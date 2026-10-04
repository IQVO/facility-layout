// OpenCode adapter for the fleet harness (harness-template v3).
// ALL logic lives in scripts/harness/hook.py (shared with Claude Code and Codex); this file only
// translates OpenCode events into that script's stdin/exit-code contract. Do not add rules here:
// add them to hook.py so the three runtimes cannot drift apart.
//
// Supports OpenCode 2.x (`setup(ctx)` + ctx.permission/ctx.tool/ctx.event hooks) AND 1.x
// (`server()` returning tool.execute.* hooks) from one default export.
// Debug: HARNESS_DEBUG_LOG=/tmp/harness.log logs every hook invocation.
import { spawnSync } from "node:child_process"
import { appendFileSync } from "node:fs"

const EDIT_TOOLS = new Set(["edit", "write", "patch", "multiedit", "apply_patch"])
const SHELL_TOOLS = new Set(["bash", "shell"])
const MAX_STOP_RETRIES = 2

const dbg = (msg: string, extra?: unknown) => {
  const f = process.env.HARNESS_DEBUG_LOG
  if (f) appendFileSync(f, `${new Date().toISOString()} ${msg} ${extra === undefined ? "" : JSON.stringify(extra)}\n`)
}

const hookRunner = (root: string) => (mode: "pre" | "post" | "stop", payload: Record<string, unknown>) => {
  const r = spawnSync("python3", [`${root}/scripts/harness/hook.py`, mode], {
    input: JSON.stringify(payload),
    encoding: "utf8",
    cwd: root,
    timeout: mode === "stop" ? 600_000 : 90_000,
  })
  dbg(`hook ${mode}`, { code: r.status, err: (r.stderr || "").slice(0, 300) })
  return { code: r.status ?? 0, message: (r.stderr || "").trim() }
}

let define: (x: any) => any = (x) => x
try {
  // @ts-ignore resolved by the OpenCode 2 runtime; absent on 1.x
  const mod = await import("@opencode/plugin")
  define = (mod as any).Plugin?.define ?? define
} catch {
  /* OpenCode 1.x: only server() is used */
}

const argsToPayload = (tool: string, args: unknown) => ({ tool_name: tool, tool_input: (args ?? {}) as Record<string, unknown> })

export default {
  ...define({
    id: "fleet.harness",
    async setup(ctx: any) {
      const root: string = ctx.location?.directory ?? process.cwd()
      const hook = hookRunner(root)
      const stopRetries = new Map<string, number>()
      const registrations: Array<{ dispose(): Promise<void> }> = []

      // PreToolUse equivalent #1: a policy-level deny whose message becomes the model's feedback.
      registrations.push(
        await ctx.permission.hook("evaluate", (event: any) => {
          const action = String(event.action ?? "").toLowerCase()
          if (!SHELL_TOOLS.has(action) && !EDIT_TOOLS.has(action)) return
          const res: string[] = (event.resources ?? []).map(String)
          const input = SHELL_TOOLS.has(action)
            ? { command: res.join(" && ") }
            : { file_path: res[0] ?? "", paths: res }
          dbg("permission.evaluate", { action, res })
          const r = hook("pre", argsToPayload(action, { ...(event.metadata ?? {}), ...input }))
          if (r.code === 2) {
            event.effect = "deny"
            event.message = r.message
          }
        }),
      )

      // PreToolUse equivalent #2 (belt and braces): throwing blocks the call.
      registrations.push(
        await ctx.tool.hook("execute.before", (event: any) => {
          dbg("tool.execute.before", { tool: event.tool })
          const r = hook("pre", argsToPayload(String(event.tool), event.input))
          if (r.code === 2) throw new Error(r.message || "blocked by harness")
        }),
      )

      // PostToolUse equivalent: append the finding to the tool result so the model self-corrects.
      registrations.push(
        await ctx.tool.hook("execute.after", (event: any) => {
          if (event.status !== "completed" || !EDIT_TOOLS.has(String(event.tool).toLowerCase())) return
          const r = hook("post", argsToPayload(String(event.tool), event.input))
          if (r.code !== 2) return
          const note = `\n\n[harness] ${r.message}`
          const res = event.result ?? {}
          event.result = { ...res, content: String(res.content ?? "") + note }
        }),
      )

      // Stop equivalent: OpenCode cannot veto "idle", so re-prompt the session (bounded retries).
      const controller = new AbortController()
      void (async () => {
        try {
          for await (const event of ctx.event.subscribe({ signal: controller.signal })) {
            if (!/idle/i.test(String(event.type))) continue
            const id = event.properties?.sessionID ?? event.data?.sessionID ?? event.sessionID
            dbg("idle event", { type: event.type, id })
            if (!id) continue
            const tries = stopRetries.get(id) ?? 0
            if (tries >= MAX_STOP_RETRIES) continue
            const r = hook("stop", { stop_hook_active: tries > 0 })
            if (r.code !== 2) continue
            stopRetries.set(id, tries + 1)
            await ctx.session.prompt({ sessionID: id, text: `[harness stop-gate] ${r.message}` })
          }
        } catch (e) {
          dbg("event loop ended", String(e))
        }
      })()

      return () => {
        controller.abort()
        registrations.forEach((r) => void r.dispose())
      }
    },
  }),

  // OpenCode 1.x
  async server({ client, worktree, directory }: any) {
    const root: string = worktree || directory
    const hook = hookRunner(root)
    const stopRetries = new Map<string, number>()
    return {
      "tool.execute.before": async (input: any, output: any) => {
        const r = hook("pre", argsToPayload(String(input.tool), output.args))
        if (r.code === 2) throw new Error(r.message || "blocked by harness")
      },
      "tool.execute.after": async (input: any, output: any) => {
        if (!EDIT_TOOLS.has(String(input.tool).toLowerCase())) return
        const r = hook("post", argsToPayload(String(input.tool), input.args))
        if (r.code === 2) output.output = `${output.output ?? ""}\n\n[harness] ${r.message}`
      },
      event: async ({ event }: any) => {
        if (event.type !== "session.idle") return
        const id = event.properties?.sessionID
        if (!id) return
        const tries = stopRetries.get(id) ?? 0
        if (tries >= MAX_STOP_RETRIES) return
        const r = hook("stop", { stop_hook_active: tries > 0 })
        if (r.code !== 2) return
        stopRetries.set(id, tries + 1)
        await client.session.prompt({ path: { id }, body: { parts: [{ type: "text", text: `[harness stop-gate] ${r.message}` }] } })
      },
    }
  },
}
