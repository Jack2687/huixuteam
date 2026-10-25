export interface User {
  id: string;
  email: string;
  name: string;
  balance: number;
  frozen: number;
  service_fee: number;
  role: "admin" | "operator" | "member";
  disabled: boolean;
  permissions: string[];
}
export interface Refund {
  id: string;
  email: string;
  status: string;
  fee: number;
  fee_status: string;
  message: string;
  created_at: string;
  updated_at: string;
}
export interface RefundEvent {
  id: number;
  message: string;
  created_at: string;
}
export interface ApiKey {
  id: string;
  name: string;
  prefix: string;
  created_at: string;
  last_used_at: string | null;
  revoked_at: string | null;
}
export interface Announcement {
  id: string; title: string; content: string;
  category: 'notice' | 'update' | 'maintenance';
  published: boolean; pinned: boolean;
  created_at: string; updated_at: string; published_at: string | null;
}
export const announcementCategories = { notice: '服务通知', update: '产品动态', maintenance: '维护安排' };
export interface Entry {
  id: number;
  kind: string;
  amount: number;
  balance_after: number;
  refund_id: string;
  note: string;
  created_at: string;
}
export interface Notification {
  id: number;
  message: string;
  read_at: string | null;
  created_at: string;
}
export interface Page<T> {
  items: T[];
  total: number;
  page: number;
  size: number;
}
export interface Dashboard {
  counts: Record<string, number>;
  trend: { date: string; count: number }[];
  provider_ready: boolean;
}
export class ApiError extends Error {
  constructor(
    message: string,
    public status: number,
  ) {
    super(message);
  }
}
export async function api<T>(
  path: string,
  options: RequestInit = {},
): Promise<T> {
  const response = await fetch(path, {
    credentials: "same-origin",
    ...options,
    headers: { "Content-Type": "application/json", ...options.headers },
  });
  const data = await response
    .json()
    .catch(() => ({ error: "服务器返回了无效响应" }));
  if (!response.ok) {
    if (response.status === 401 && !path.includes("/auth/"))
      window.dispatchEvent(new Event("session-expired"));
    throw new ApiError(
      data.error || `请求失败 (${response.status})`,
      response.status,
    );
  }
  return data;
}
export const money = (value = 0) =>
  (value / 100).toLocaleString("zh-CN", { maximumFractionDigits: 2 });
export const date = (value: string) =>
  new Intl.DateTimeFormat("zh-CN", {
    timeZone: "Asia/Shanghai",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
  }).format(new Date(value));
export const statuses: Record<string, { label: string; color: string }> = {
  queued: { label: "排队中", color: "amber" },
  processing: { label: "处理中", color: "blue" },
  verifying: { label: "核验中", color: "purple" },
  succeeded: { label: "已完成", color: "green" },
  attention: { label: "待跟进", color: "orange" },
  banned: { label: "已封号", color: "red" },
  failed: { label: "未完成", color: "red" },
  cancelled: { label: "已取消", color: "gray" },
};
export const feeLabel: Record<string, string> = {
  frozen: "已冻结",
  settled: "已结算",
  released: "已释放",
};
export async function copy(text: string) {
  if (navigator.clipboard && window.isSecureContext) {
    await navigator.clipboard.writeText(text);
    return;
  }
  const input = document.createElement("textarea");
  input.value = text;
  input.style.cssText = "position:fixed;opacity:0";
  document.body.append(input);
  input.select();
  try {
    if (!document.execCommand("copy"))
      throw new Error("复制失败，请手动选择复制");
  } finally {
    input.remove();
  }
}
export function exportTXT(lines: string[], filename: string) {
  const blob = new Blob([lines.join("\r\n")], { type: "text/plain;charset=utf-8" });
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = filename;
  link.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}
export function exportCSV(
  rows: string[][],
  filename = `退款申请-${new Date().toISOString().slice(0, 10)}.csv`,
) {
  const escape = (value: string) =>
    '"' +
    (/^[=+\-@\t\r]/.test(value) ? "'" + value : value).replaceAll('"', '""') +
    '"';
  const blob = new Blob(
    ["\uFEFF" + rows.map((row) => row.map(escape).join(",")).join("\r\n")],
    { type: "text/csv;charset=utf-8" },
  );
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = filename;
  link.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}
