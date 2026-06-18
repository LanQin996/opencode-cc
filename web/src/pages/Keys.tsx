import { useEffect, useState } from "react";
import { api, APIKey, KeyBody } from "../lib/api";
import { fmtNum } from "../lib/format";
import { PageHeader } from "../App";
import { Badge, Card, EmptyState, Spinner } from "../components/ui";

const EMPTY: KeyBody = {
  name: "",
  enabled: true,
  token_quota: 0,
  request_quota: 0,
  daily_token_limit: 0,
  daily_request_limit: 0,
  allowed_ips: "",
  expires_at: 0,
};

export default function Keys() {
  const [keys, setKeys] = useState<APIKey[]>([]);
  const [loading, setLoading] = useState(true);
  const [editing, setEditing] = useState<APIKey | null>(null);
  const [creating, setCreating] = useState(false);
  const [createdPlain, setCreatedPlain] = useState<string | null>(null);
  const [flash, setFlash] = useState("");

  async function refresh() {
    try {
      setKeys(await api.keys());
    } finally {
      setLoading(false);
    }
  }
  useEffect(() => {
    refresh();
  }, []);

  function notify(msg: string) {
    setFlash(msg);
    setTimeout(() => setFlash(""), 2500);
  }

  async function handleCreate(body: KeyBody) {
    const created = await api.createKey(body);
    setCreatedPlain(created.plain_key);
    setCreating(false);
    await refresh();
    notify("密钥已创建");
  }
  async function handleUpdate(id: number, body: KeyBody) {
    await api.updateKey(id, body);
    setEditing(null);
    await refresh();
    notify("已保存");
  }
  async function handleDelete(id: number) {
    if (!confirm("确定删除这个密钥？此操作不可撤销。")) return;
    await api.deleteKey(id);
    await refresh();
    notify("已删除");
  }
  async function handleReset(id: number) {
    await api.resetKey(id);
    await refresh();
    notify("用量已重置");
  }

  return (
    <div className="animate-fade-in">
      <PageHeader
        title="API 密钥"
        desc="为客户端签发密钥，可设置额度与 IP 白名单。"
        actions={
          <div className="flex items-center gap-3">
            {flash && <span className="text-xs text-accent-green">{flash}</span>}
            <button onClick={() => setCreating(true)} className="btn-primary">
              <PlusIcon /> 新建密钥
            </button>
          </div>
        }
      />

      {/* How-to hint */}
      <Card className="mb-4">
        <div className="flex items-start gap-3">
          <div className="mt-0.5 text-accent-glow">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
              <circle cx="12" cy="12" r="10" />
              <path d="M12 16v-4M12 8h.01" strokeLinecap="round" />
            </svg>
          </div>
          <div className="text-sm text-slate-400">
            客户端把密钥放在 <code className="text-accent-glow font-mono">Authorization: Bearer &lt;密钥&gt;</code>。
            在 Claude Code 里：<code className="text-slate-300 font-mono">set ANTHROPIC_AUTH_TOKEN=sk-xxxx</code>。
            启用鉴权需在「设置」页打开「要求 API 密钥」。
          </div>
        </div>
      </Card>

      {loading ? (
        <div className="flex items-center justify-center py-16 text-slate-500 gap-2">
          <Spinner /> 加载中…
        </div>
      ) : keys.length === 0 ? (
        <Card>
          <EmptyState
            title="还没有 API 密钥"
            hint="新建一个密钥供客户端访问代理；开启鉴权前请至少创建一个密钥。"
          />
        </Card>
      ) : (
        <div className="space-y-3">
          {keys.map((k) => (
            <KeyRow
              key={k.id}
              k={k}
              onEdit={() => setEditing(k)}
              onDelete={() => handleDelete(k.id)}
              onReset={() => handleReset(k.id)}
            />
          ))}
        </div>
      )}

      {(creating || editing) && (
        <KeyModal
          initial={editing}
          onClose={() => {
            setCreating(false);
            setEditing(null);
          }}
          onSubmit={(body) => (editing ? handleUpdate(editing.id, body) : handleCreate(body))}
        />
      )}

      {createdPlain && (
        <PlainKeyModal plain={createdPlain} onClose={() => setCreatedPlain(null)} />
      )}
    </div>
  );
}

function KeyRow({
  k,
  onEdit,
  onDelete,
  onReset,
}: {
  k: APIKey;
  onEdit: () => void;
  onDelete: () => void;
  onReset: () => void;
}) {
  const tokenPct = k.token_quota > 0 ? Math.min(100, (k.used_tokens / k.token_quota) * 100) : 0;
  const reqPct = k.request_quota > 0 ? Math.min(100, (k.used_requests / k.request_quota) * 100) : 0;

  return (
    <Card className="!p-4">
      <div className="flex flex-wrap items-start justify-between gap-4">
        {/* Left: identity */}
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2 flex-wrap">
            <span className="font-mono text-sm text-slate-200">{k.key_prefix}…</span>
            {k.enabled ? <Badge tone="green">启用</Badge> : <Badge tone="red">已停用</Badge>}
            {(() => {
              const e = expiryBadge(k.expires_at);
              return e ? <Badge tone={e.tone}>{e.text}</Badge> : null;
            })()}
            {k.name && <span className="text-sm text-slate-400">· {k.name}</span>}
          </div>
          <div className="mt-2 flex flex-wrap gap-x-5 gap-y-1 text-xs text-slate-500">
            <span>累计 <span className="text-slate-300 font-mono">{fmtNum(k.used_tokens)}</span> tok · <span className="text-slate-300 font-mono">{fmtNum(k.used_requests)}</span> 次</span>
            <span>今日 <span className="text-accent-cyan font-mono">{fmtNum(k.daily_used_tokens)}</span> tok · <span className="text-accent-cyan font-mono">{fmtNum(k.daily_used_requests)}</span> 次</span>
            {k.allowed_ips && <span>IP 白名单：<span className="font-mono text-slate-400">{k.allowed_ips}</span></span>}
          </div>
        </div>

        {/* Right: actions */}
        <div className="flex items-center gap-1.5 shrink-0">
          <button onClick={onReset} className="btn-ghost !py-1.5 !px-2.5 !text-xs" title="重置用量">
            重置
          </button>
          <button onClick={onEdit} className="btn-ghost !py-1.5 !px-2.5 !text-xs">编辑</button>
          <button onClick={onDelete} className="btn-ghost !py-1.5 !px-2.5 !text-xs text-accent-red">删除</button>
        </div>
      </div>

      {/* Quota bars */}
      {(k.token_quota > 0 || k.request_quota > 0) && (
        <div className="mt-3 grid grid-cols-1 sm:grid-cols-2 gap-3">
          {k.token_quota > 0 && (
            <QuotaBar label="Token 总额度" used={k.used_tokens} quota={k.token_quota} pct={tokenPct} />
          )}
          {k.request_quota > 0 && (
            <QuotaBar label="请求次数额度" used={k.used_requests} quota={k.request_quota} pct={reqPct} />
          )}
        </div>
      )}
      {(k.daily_token_limit > 0 || k.daily_request_limit > 0) && (
        <div className="mt-3 grid grid-cols-1 sm:grid-cols-2 gap-3">
          {k.daily_token_limit > 0 && (
            <QuotaBar label="今日 Token 限额" used={k.daily_used_tokens} quota={k.daily_token_limit} pct={Math.min(100, (k.daily_used_tokens / k.daily_token_limit) * 100)} />
          )}
          {k.daily_request_limit > 0 && (
            <QuotaBar label="今日请求限额" used={k.daily_used_requests} quota={k.daily_request_limit} pct={Math.min(100, (k.daily_used_requests / k.daily_request_limit) * 100)} />
          )}
        </div>
      )}
    </Card>
  );
}

function QuotaBar({ label, used, quota, pct }: { label: string; used: number; quota: number; pct: number }) {
  const tone = pct > 90 ? "from-accent-red/70 to-accent-red" : pct > 70 ? "from-accent-amber/70 to-accent-amber" : "from-accent/70 to-accent-cyan/60";
  return (
    <div>
      <div className="flex justify-between text-[11px] text-slate-500 mb-1">
        <span>{label}</span>
        <span className="font-mono">{fmtNum(used)} / {fmtNum(quota)}</span>
      </div>
      <div className="h-2 rounded-full bg-white/[0.04] overflow-hidden">
        <div className={`h-full bg-gradient-to-r ${tone} transition-all`} style={{ width: `${pct}%` }} />
      </div>
    </div>
  );
}

function KeyModal({
  initial,
  onClose,
  onSubmit,
}: {
  initial: APIKey | null;
  onClose: () => void;
  onSubmit: (body: KeyBody) => void;
}) {
  const [form, setForm] = useState<KeyBody>(
    initial
      ? {
          name: initial.name,
          enabled: initial.enabled,
          token_quota: initial.token_quota,
          request_quota: initial.request_quota,
          daily_token_limit: initial.daily_token_limit,
          daily_request_limit: initial.daily_request_limit,
          allowed_ips: initial.allowed_ips,
          expires_at: initial.expires_at,
        }
      : EMPTY
  );
  const [saving, setSaving] = useState(false);

  // Expiry editor state: derive a friendly mode from the epoch value.
  // modes: "never" | "days" | "date"
  const [expMode, setExpMode] = useState<"never" | "days" | "date">(() => {
    if (!form.expires_at) return "never";
    return "date";
  });
  const [expDays, setExpDays] = useState<number>(30);
  // datetime-local expects yyyy-MM-ddTHH:mm in LOCAL time.
  const [expDate, setExpDate] = useState<string>(() => {
    if (!form.expires_at) return "";
    const d = new Date(form.expires_at * 1000);
    const pad = (n: number) => String(n).padStart(2, "0");
    return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
  });

  function set<K extends keyof KeyBody>(key: K, val: KeyBody[K]) {
    setForm((f) => ({ ...f, [key]: val }));
  }

  // Recompute expires_at from the chosen mode whenever the mode/inputs change.
  function commitExpiry(mode: "never" | "days" | "date", days = expDays, date = expDate) {
    setExpMode(mode);
    if (mode === "never") {
      set("expires_at", 0);
    } else if (mode === "days") {
      const ts = Math.floor(Date.now() / 1000) + Math.max(1, Math.floor(days)) * 86400;
      set("expires_at", ts);
    } else {
      const t = new Date(date).getTime();
      set("expires_at", isNaN(t) ? 0 : Math.floor(t / 1000));
    }
  }

  async function submit() {
    setSaving(true);
    try {
      await onSubmit(form);
    } finally {
      setSaving(false);
    }
  }

  return (
    <div className="fixed inset-0 z-40 flex items-center justify-center p-4">
      <div className="absolute inset-0 bg-black/50 backdrop-blur-sm" onClick={onClose} />
      <div className="relative w-full max-w-lg glass p-6 animate-fade-in max-h-[90vh] overflow-y-auto">
        <div className="flex items-center justify-between mb-5">
          <h3 className="text-base font-semibold text-white">{initial ? "编辑密钥" : "新建 API 密钥"}</h3>
          <button onClick={onClose} className="btn-ghost !px-2.5 !py-2">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><path d="M18 6L6 18M6 6l12 12" strokeLinecap="round" /></svg>
          </button>
        </div>

        <div className="space-y-4">
          <div>
            <label className="label">备注名</label>
            <input className="input" placeholder="例如：我的电脑 / 同事A" value={form.name} onChange={(e) => set("name", e.target.value)} />
          </div>

          <label className="flex items-center justify-between cursor-pointer">
            <span className="text-sm text-slate-200">启用此密钥</span>
            <button type="button" onClick={() => set("enabled", !form.enabled)} className={`relative w-11 h-6 rounded-full transition-colors ${form.enabled ? "bg-accent" : "bg-ink-600"}`}>
              <span className={`absolute top-0.5 left-0.5 w-5 h-5 rounded-full bg-white transition-transform ${form.enabled ? "translate-x-5" : ""}`} />
            </button>
          </label>

          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className="label">Token 总额度（0=不限）</label>
              <input type="number" min={0} className="input font-mono" value={form.token_quota} onChange={(e) => set("token_quota", Number(e.target.value))} />
            </div>
            <div>
              <label className="label">请求次数额度（0=不限）</label>
              <input type="number" min={0} className="input font-mono" value={form.request_quota} onChange={(e) => set("request_quota", Number(e.target.value))} />
            </div>
            <div>
              <label className="label">每日 Token 限额（0=不限）</label>
              <input type="number" min={0} className="input font-mono" value={form.daily_token_limit} onChange={(e) => set("daily_token_limit", Number(e.target.value))} />
            </div>
            <div>
              <label className="label">每日请求限额（0=不限）</label>
              <input type="number" min={0} className="input font-mono" value={form.daily_request_limit} onChange={(e) => set("daily_request_limit", Number(e.target.value))} />
            </div>
          </div>

          <div>
            <label className="label">有效期</label>
            <div className="flex gap-2 flex-wrap items-center">
              <select
                className="input !w-auto"
                value={expMode}
                onChange={(e) => {
                  const m = e.target.value as "never" | "days" | "date";
                  if (m === "never") commitExpiry("never");
                  else if (m === "days") commitExpiry("days", 30);
                  else commitExpiry("date", expDays, expDate || tomorrowLocal());
                }}
              >
                <option value="never">永久有效</option>
                <option value="days">N 天后过期</option>
                <option value="date">指定日期</option>
              </select>
              {expMode === "days" && (
                <div className="flex items-center gap-2">
                  <input
                    type="number"
                    min={1}
                    className="input font-mono !w-24"
                    value={expDays}
                    onChange={(e) => {
                      const n = Number(e.target.value);
                      setExpDays(n);
                      commitExpiry("days", n);
                    }}
                  />
                  <span className="text-xs text-slate-500">天</span>
                </div>
              )}
              {expMode === "date" && (
                <input
                  type="datetime-local"
                  className="input font-mono !w-auto"
                  value={expDate}
                  onChange={(e) => {
                    setExpDate(e.target.value);
                    commitExpiry("date", expDays, e.target.value);
                  }}
                />
              )}
            </div>
            <p className="text-xs text-slate-500 mt-1.5">
              {form.expires_at
                ? `将于 ${new Date(form.expires_at * 1000).toLocaleString()} 过期`
                : "此密钥不会过期。"}
            </p>
          </div>

          <div>
            <label className="label">IP 白名单（逗号分隔 CIDR，留空=不限）</label>
            <input className="input font-mono" placeholder="例如：1.2.3.4, 10.0.0.0/8" value={form.allowed_ips} onChange={(e) => set("allowed_ips", e.target.value)} />
            <p className="text-xs text-slate-500 mt-1.5">支持单 IP 和 CIDR 网段；仅信任来自本机反向代理的 X-Forwarded-For。</p>
          </div>
        </div>

        <div className="flex justify-end gap-2 mt-6">
          <button onClick={onClose} className="btn-ghost">取消</button>
          <button onClick={submit} disabled={saving} className="btn-primary">
            {saving ? <Spinner /> : null}
            {initial ? "保存" : "创建"}
          </button>
        </div>
      </div>
    </div>
  );
}

function PlainKeyModal({ plain, onClose }: { plain: string; onClose: () => void }) {
  const [copied, setCopied] = useState(false);
  function copy() {
    navigator.clipboard?.writeText(plain);
    setCopied(true);
    setTimeout(() => setCopied(false), 1500);
  }
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4">
      <div className="absolute inset-0 bg-black/60 backdrop-blur-sm" />
      <div className="relative w-full max-w-lg glass p-6 animate-fade-in">
        <div className="flex items-center gap-2 mb-2">
          <div className="w-8 h-8 rounded-lg bg-accent-green/15 border border-accent-green/30 flex items-center justify-center">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="#34d399" strokeWidth="2.5"><path d="M20 6L9 17l-5-5" strokeLinecap="round" strokeLinejoin="round" /></svg>
          </div>
          <h3 className="text-base font-semibold text-white">密钥已创建</h3>
        </div>
        <p className="text-sm text-accent-amber mb-4">
          ⚠️ 这是该密钥的完整明文，<b>仅显示这一次</b>。请立即复制保存，之后无法再次查看。
        </p>
        <div className="flex gap-2">
          <code className="flex-1 rounded-xl bg-ink-950/80 border border-white/[0.06] px-3 py-2.5 font-mono text-sm text-accent-glow break-all">
            {plain}
          </code>
          <button onClick={copy} className="btn-primary shrink-0">
            {copied ? "已复制" : "复制"}
          </button>
        </div>
        <div className="flex justify-end mt-5">
          <button onClick={onClose} className="btn-ghost">我已保存</button>
        </div>
      </div>
    </div>
  );
}

function PlusIcon() {
  return (
    <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5">
      <path d="M12 5v14M5 12h14" strokeLinecap="round" />
    </svg>
  );
}

// tomorrowLocal returns a yyyy-MM-ddTHH:mm string for ~24h from now, used as a
// sensible default when switching to the "specific date" expiry mode.
function tomorrowLocal(): string {
  const d = new Date(Date.now() + 24 * 3600 * 1000);
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

// expiryBadge returns a {text, tone} descriptor for a key's expiry state, or
// null when the key never expires.
function expiryBadge(expiresAt: number): { text: string; tone: "amber" | "red" | "cyan" } | null {
  if (!expiresAt) return null;
  const now = Date.now();
  const exp = expiresAt * 1000;
  const diff = exp - now;
  if (diff <= 0) return { text: "已过期", tone: "red" };
  if (diff < 24 * 3600 * 1000) return { text: `${Math.max(1, Math.floor(diff / 3600000))}小时后过期`, tone: "red" };
  const days = Math.floor(diff / (24 * 3600 * 1000));
  if (days <= 7) return { text: `${days}天后过期`, tone: "amber" };
  const d = new Date(exp);
  return {
    text: `${d.getMonth() + 1}/${d.getDate()} 到期`,
    tone: "cyan",
  };
}
