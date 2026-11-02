# ADR-0007: autorun 以 sh -c 包裹，与既有命令共存

- 状态：已接受（#9）
- 背景：qBittorrent Linux autorun 是 argv 拆分不走 shell，原生只支持一条命令；生产已有 moviepilot 的 autorun curl 在用。
- 决策：向导把 autorun_program 重写为 `/bin/sh -c "curl '…moviepilot…' ; curl -fsS -m 10 '…/api/hooks/qbt?ih=%I'"`；外层双引号、payload 内单引号、`;` 独立执行；幂等（现值已含本项目 URL 则跳过）；改写前展示前后完整对比人工确认；不碰 on-added 钩子；唯一变量 %I 纯 hex。
- 后果：双命令共存不互踢；注入面收敛到十六进制单变量。规格见 §5.4。
