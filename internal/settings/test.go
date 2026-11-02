package settings

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// TestCode 是「测试连接」的稳定机器码（规格 §8.4）。
//
// 前端只认这些码来决定 chip 的颜色与文案，不要往里塞自然语言。
type TestCode string

const (
	CodeTimeout         TestCode = "timeout"
	CodeAuth            TestCode = "auth"
	CodeNetwork         TestCode = "network"
	CodeProtocol        TestCode = "protocol"
	CodeBusiness        TestCode = "business"
	CodeTokenInvalid    TestCode = "token_invalid"
	CodeChatUnavailable TestCode = "chat_unavailable"

	// CodeNotImplemented 是占位码：这一节的真实测试逻辑由集成票落地。
	//
	// 它不算一次真实测试，所以不写 last_test_*（否则向导会把「还没做」显示成红叉）。
	CodeNotImplemented TestCode = "not_implemented"
)

// TestTimeout 是任何一次测试连接的上限（规格 §8.4：超时一律 10s）。
const TestTimeout = 10 * time.Second

// ErrNotTestable 说明这一节没有「测试连接」这回事（app / list_registry）。
var ErrNotTestable = errors.New("settings: 这一节不能测试连接")

// ErrNotConfigured 说明这一节还没保存过，谈不上测试。
var ErrNotConfigured = errors.New("settings: 这一节还没保存过")

// TestResult 是一次测试连接的判词。
type TestResult struct {
	OK       bool
	Code     TestCode
	Detail   string
	Duration time.Duration
}

// TestInput 是测试连接要用的原文：登录凭据就在 Values 里（对外的掩码在 SectionState，
// 那是给人看的，测试必须拿到真的才能连上游）。
type TestInput struct {
	// Values 是这一节的字段原文；TypeDuration 已转成 time.Duration。
	Values map[string]any
	// Enabled 是可选集成的开关（节上没有 enabled 时为 false）。
	Enabled bool
	// Lists 是已登记的清单（list_registry 的全部行）。JavDB 的测试要读登记清单，
	// 但那是另一节，所以由 Store 读好一并交过来。
	Lists []ListEntry
}

// ListFor 按用途取一张已登记的清单。
func (in TestInput) ListFor(purpose string) (ListEntry, bool) {
	for _, entry := range in.Lists {
		if entry.Purpose == purpose {
			return entry, true
		}
	}
	return ListEntry{}, false
}

// Text 取一个文本字段的原文。
func (in TestInput) Text(name string) string {
	text, _ := in.Values[name].(string)
	return text
}

// Bool 取一个布尔字段。
func (in TestInput) Bool(name string) bool {
	value, _ := in.Values[name].(bool)
	return value
}

// Duration 取一个 interval 字段。
func (in TestInput) Duration(name string) time.Duration {
	value, _ := in.Values[name].(time.Duration)
	return value
}

// Tester 测一节集成是否可用。实现自己负责用 ctx 的 deadline，并且只做读操作——
// 测试按钮不许改上游状态。
//
// Detail 会原样回给前端并写进 last_test_error：写错误分类与摘要，绝不写凭据原文。
type Tester interface {
	Test(ctx context.Context, section Section, input TestInput) TestResult
}

// TesterFunc 让函数直接当 Tester 用。
type TesterFunc func(ctx context.Context, section Section, input TestInput) TestResult

// Test 实现 Tester。
func (f TesterFunc) Test(ctx context.Context, section Section, input TestInput) TestResult {
	return f(ctx, section, input)
}

// Option 在装配 Store 时替换某一节的测试实现。
type Option func(*Store)

// WithTester 换掉某一节的测试实现（集成票接线点）。
func WithTester(section string, tester Tester) Option {
	return func(s *Store) { s.testers[section] = tester }
}

// WithTestTimeout 覆盖测试连接的上限：正式运行永远是 10s，测试用它把超时路径跑快。
func WithTestTimeout(timeout time.Duration) Option {
	return func(s *Store) { s.testTimeout = timeout }
}

// placeholderTester 是集成接线前的明确占位：说清楚「还没接上」，不假装成功也不假装失败。
func placeholderTester(_ context.Context, section Section, _ TestInput) TestResult {
	return TestResult{
		Code:   CodeNotImplemented,
		Detail: section.Name + " 的测试连接还没实现（对应的集成票落地后点亮）；本步配置已保存生效",
	}
}

// Test 跑一节的测试连接，并把真实结果写进 last_test_*。
//
// 10s 超时由这里统一保证：测试实现在 goroutine 里跑，超时就把结论定成 timeout，
// 任何一节都不会因为上游挂住而把请求拖死。
func (s *Store) Test(ctx context.Context, name string) (TestResult, error) {
	section, ok := Lookup(name)
	if !ok {
		return TestResult{}, ErrUnknownSection
	}
	if section.Kind != KindIntegration {
		return TestResult{}, ErrNotTestable
	}

	loaded, err := loadRow(ctx, s.pool, section, false)
	if err != nil {
		return TestResult{}, err
	}
	if !loaded.configured {
		return TestResult{}, ErrNotConfigured
	}
	input, err := s.inputFrom(ctx, loaded)
	if err != nil {
		return TestResult{}, err
	}

	tester, ok := s.testers[name]
	if !ok {
		return TestResult{}, fmt.Errorf("settings: %s 没有注册测试实现", name)
	}

	tested := runTest(ctx, tester, section, input, s.testTimeout)

	if tested.Code == CodeNotImplemented {
		return tested, nil
	}

	recordCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.testTimeout)
	defer cancel()
	if err := s.RecordTest(recordCtx, name, tested); err != nil {
		return tested, err
	}
	return tested, nil
}

func runTest(ctx context.Context, tester Tester, section Section, input TestInput, timeout time.Duration) TestResult {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	started := time.Now()
	done := make(chan TestResult, 1)
	go func() { done <- tester.Test(ctx, section, input) }()

	var result TestResult
	select {
	case result = <-done:
		if ctx.Err() != nil && result.OK {
			// 上游在超时边缘回了成功：宁可按超时报，也不给一个可能已作废的绿。
			result = TestResult{Code: CodeTimeout, Detail: fmt.Sprintf("测试在 %s 内没有返回", timeout)}
		}
	case <-ctx.Done():
		result = TestResult{Code: CodeTimeout, Detail: fmt.Sprintf("测试在 %s 内没有返回", timeout)}
	}

	result.Duration = time.Since(started)
	return result
}

// Input 读一节的原文（含凭据）与已登记清单，供集成层装配客户端。
//
// 它是**内部接线点**，不是 HTTP 面：返回值里有凭据原文，只许在进程内流转。
// 对外的读写一律走 Section/Patch（敏感列只出掩码）。
func (s *Store) Input(ctx context.Context, name string) (TestInput, error) {
	section, ok := Lookup(name)
	if !ok {
		return TestInput{}, ErrUnknownSection
	}
	loaded, err := loadRow(ctx, s.pool, section, false)
	if err != nil {
		return TestInput{}, err
	}
	if !loaded.configured {
		return TestInput{}, ErrNotConfigured
	}
	return s.inputFrom(ctx, loaded)
}

// inputFrom 把一行原文装成 TestInput（含已登记清单）。
//
// Test 与 Input 共用它：两处装配必须一致，否则测试看到的输入会和实际连上游时的不一样。
// 清单登记是另一节，JavDB 的「测试读取」要连它一起验，所以这里一并读好。
func (s *Store) inputFrom(ctx context.Context, loaded row) (TestInput, error) {
	enabled, _ := loaded.values["enabled"].(bool)
	lists, err := s.registeredLists(ctx)
	if err != nil {
		return TestInput{}, err
	}
	return TestInput{Values: loaded.values, Enabled: enabled, Lists: lists}, nil
}
