// PROTOTYPE ONLY — fixture data mirroring the domain decisions in #7/#8/#9/#10.
// Nothing here is fetched; every number is hand-written to make the UI judgeable.

export type TaskState = 'downloading' | 'processing' | 'finished' | 'failed';

export const stateMeta: Record<
  TaskState,
  { label: string; tone: 'info' | 'warn' | 'ok' | 'bad'; dot: 'busy' | 'warn' | 'ok' | 'bad' }
> = {
  downloading: { label: '下载中', tone: 'info', dot: 'busy' },
  processing: { label: '加工中', tone: 'warn', dot: 'warn' },
  finished: { label: '已入库', tone: 'ok', dot: 'ok' },
  failed: { label: '需人工', tone: 'bad', dot: 'bad' },
};

export type VersionState =
  | { state: 'downloading' }
  | { state: 'processing'; phase: 'scrape' | 'organize' }
  | { state: 'finished' }
  | { state: 'failed'; phase: 'downloading' | 'scrape' | 'organize'; reason: string; attempts: number };

export interface Version {
  infohash: string;
  label: string;
  source: 'javdb_magnet' | 'mteam_torrent';
  detail: VersionState;
}

export interface Task {
  movieId: string;
  number: string;
  title: string;
  versions: Version[];
  listName: string;
  updatedAt: string;
  note?: string;
}

export interface TaskEvent {
  at: string;
  kind: 'state_change' | 'side_effect' | 'alert' | 'manual_retry';
  text: string;
}

export interface SideEffect {
  kind: 'list_removal' | 'jellyfin_refresh' | 'notify';
  target: string;
  attempts: number;
  nextAttempt: string;
  lastError?: string;
}

export const tasks: Task[] = [
  {
    movieId: 'javdb-9k2a',
    number: 'KV-328',
    title: '新人 NO.1 STYLE 専属デビュー',
    versions: [
      {
        infohash: 'a1b2c3d4e5f60718293a4b5c6d7e8f9012345678',
        label: '普通',
        source: 'mteam_torrent',
        detail: { state: 'finished' },
      },
      {
        infohash: 'bb11cc22dd33ee44ff5500112233445566778899',
        label: '中字',
        source: 'javdb_magnet',
        detail: { state: 'finished' },
      },
    ],
    listName: '想看',
    updatedAt: '3 分钟前',
    note: '第二个版本走了「复用元数据」：0 次网络，只硬链接 + 拷 NFO',
  },
  {
    movieId: 'javdb-4m8c',
    number: 'VRVR-456',
    title: 'VR 8K 完全主観 3 部作',
    versions: [
      {
        infohash: 'cc00dd11ee22ff33445566778899aabbccddeeff',
        label: '普通',
        source: 'mteam_torrent',
        detail: { state: 'processing', phase: 'organize' },
      },
    ],
    listName: '想看',
    updatedAt: '40 秒前',
    note: '同一种子内 cd1/cd2/cd3 三个分片文件，正在按 part_index 分组',
  },
  {
    movieId: 'javdb-7p1d',
    number: 'MIDE-777',
    title: '【数量限定】密着 120 分',
    versions: [
      {
        infohash: '11aa22bb33cc44dd55ee66ff77008899aabbccdd',
        label: '普通',
        source: 'javdb_magnet',
        detail: { state: 'downloading' },
      },
    ],
    listName: '想看',
    updatedAt: '5 分钟前',
  },
  {
    movieId: 'javdb-2x5e',
    number: 'SSIS-001',
    title: '新人 19 歳 AV デビュー',
    versions: [
      {
        infohash: 'eeff00112233445566778899aabbccddeeff0011',
        label: '普通',
        source: 'javdb_magnet',
        detail: {
          state: 'failed',
          phase: 'scrape',
          reason: 'DMM 播放页 GraphQL 返回 500（已尝试 3 次）',
          attempts: 3,
        },
      },
    ],
    listName: '想看',
    updatedAt: '18 分钟前',
  },
  {
    movieId: 'javdb-6r3f',
    number: 'IPX-900',
    title: '交わる体液、濃密セックス',
    versions: [
      {
        infohash: '99887766554433221100ffeeddccbbaa99887766',
        label: '普通',
        source: 'javdb_magnet',
        detail: {
          state: 'failed',
          phase: 'downloading',
          reason: '种子已从 qBittorrent 移除（对账发现），等待人工重试',
          attempts: 1,
        },
      },
    ],
    listName: '想看',
    updatedAt: '1 小时前',
  },
  {
    movieId: 'javdb-8t7g',
    number: 'SNIS-222',
    title: '【VR】没入 完全版',
    versions: [
      {
        infohash: '1234abcd5678ef901234abcd5678ef901234abcd',
        label: '普通',
        source: 'mteam_torrent',
        detail: { state: 'finished' },
      },
    ],
    listName: '想看',
    updatedAt: '7 分钟前',
    note: '入库已完成，Jellyfin 刷新旁路仍在重试',
  },
];

export const events: TaskEvent[] = [
  { at: '刚刚', kind: 'manual_retry', text: 'SSIS-001 人工重试：state failed → processing（scrape）' },
  { at: '40 秒前', kind: 'state_change', text: 'VRVR-456 → processing（organize）' },
  { at: '2 分钟前', kind: 'side_effect', text: 'KV-328 清单项删除成功（清单 48213）' },
  { at: '3 分钟前', kind: 'state_change', text: 'KV-328 中字版本入库 → task finished' },
  { at: '18 分钟前', kind: 'alert', text: 'SSIS-001 刮削失败 ×3，Telegram 已通知（仅一次）' },
  { at: '1 小时前', kind: 'alert', text: 'IPX-900 种子从 qBittorrent 消失，版本置 failed(下载阶段)' },
  { at: '2 小时前', kind: 'side_effect', text: 'Jellyfin 刷新合并：SNIS-222 与 KV-328 合并为一次目录级刷新' },
];

export const sideEffects: SideEffect[] = [
  {
    kind: 'jellyfin_refresh',
    target: 'SNIS-222',
    attempts: 2,
    nextAttempt: '12 秒后',
    lastError: 'jellyfin: 502 Bad Gateway',
  },
  { kind: 'list_removal', target: '48213:javdb-7p1d', attempts: 0, nextAttempt: '排队中' },
  { kind: 'notify', target: 'javdb-9k2a:入库完成', attempts: 0, nextAttempt: '排队中' },
];

export const sideEffectLabel: Record<SideEffect['kind'], string> = {
  list_removal: '清单项删除',
  jellyfin_refresh: 'Jellyfin 刷新',
  notify: 'Telegram 通知',
};

export const loops = [
  { name: '清单轮询', interval: 15, last: '2 分钟前', state: 'ok' as const },
  { name: '对账', interval: 5, last: '40 秒前', state: 'ok' as const },
  { name: '副作用', interval: 1, last: '20 秒前', state: 'warn' as const },
];

/* --------------------------------- wizard ---------------------------------- */

export interface WizardForm {
  javdb: { token: string; listMode: 'existing' | 'create'; listName: string; listId: string };
  qbt: {
    url: string;
    username: string;
    password: string;
    category: string;
    downloadRoot: string;
    baseUrl: string;
  };
  jellyfin: { url: string; apiKey: string; mediaRoot: string };
  mteam: { enabled: boolean; apiKey: string };
  ai: { enabled: boolean; baseUrl: string; apiKey: string; model: string };
  telegram: { enabled: boolean; botToken: string; chatId: string };
  advanced: { poll: string; reconcile: string; sidefx: string; maxAttempts: string };
}

export const initialForm: WizardForm = {
  javdb: {
    token: '••••••••••••3f9c',
    listMode: 'existing',
    listName: '想看',
    listId: '48213',
  },
  qbt: {
    url: 'http://qbittorrent-main.qbittorrent-private.svc:8082',
    username: '',
    password: '',
    category: 'mdc',
    downloadRoot: '/downloads/mdc',
    baseUrl: 'http://oh-my-mdc.media.svc.cluster.local:8080',
  },
  jellyfin: {
    url: 'http://jellyfin.media.svc.cluster.local:8096',
    apiKey: '••••••••••••a71e',
    mediaRoot: '/media/library/adult',
  },
  mteam: { enabled: false, apiKey: '' },
  ai: {
    enabled: true,
    baseUrl: 'https://api.openai.com/v1',
    apiKey: '••••••••••••88d0',
    model: 'gpt-4o-mini',
  },
  telegram: { enabled: false, botToken: '', chatId: '' },
  advanced: { poll: '15', reconcile: '5', sidefx: '1', maxAttempts: '3' },
};

export const autorunBefore =
  "curl 'http://moviepilot.media.svc:3001/api/v1/transfer/now?token=5f2c9a'";
export const autorunAfter =
  `/bin/sh -c "curl 'http://moviepilot.media.svc:3001/api/v1/transfer/now?token=5f2c9a' ; curl -fsS -m 10 'http://oh-my-mdc.media.svc.cluster.local:8080/api/hooks/qbt?ih=%I'"`;

export const wizardSteps = [
  { key: 'javdb', title: 'JavDB 账号与清单', required: true },
  { key: 'qbt', title: 'qBittorrent', required: true },
  { key: 'jellyfin', title: 'Jellyfin', required: true },
  { key: 'mteam', title: 'M-Team（可选）', required: false },
  { key: 'ai', title: 'AI 翻译（可选）', required: false },
  { key: 'telegram', title: 'Telegram（可选）', required: false },
  { key: 'done', title: '完成', required: true },
] as const;
