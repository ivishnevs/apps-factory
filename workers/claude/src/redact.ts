// Progress event payloads: OpenSpec document bodies stay in the workspace (see
// openspec.AuditData in Go) and oversized messages are bounded below broker limits.
const OPENSPEC_READ = "mcp__factory__factory_openspec_read";
export const MAX_EVENT_DATA = 256 * 1024;

type Block = { type?: string; id?: string; name?: string; tool_use_id?: string; content?: unknown };

export class Redactor {
  private readonly openspecCalls = new Set<string>();

  data(message: unknown): unknown {
    const m = message as { type?: string; message?: { content?: unknown }; tool_use_result?: unknown };
    const blocks = Array.isArray(m.message?.content) ? (m.message.content as Block[]) : [];
    let out = message;
    if (m.type === "assistant") {
      for (const b of blocks) {
        if (b.type === "tool_use" && b.name === OPENSPEC_READ && b.id) this.openspecCalls.add(b.id);
      }
    } else if (m.type === "user" && blocks.some((b) => b.type === "tool_result" && this.openspecCalls.has(b.tool_use_id ?? ""))) {
      const omitted = { document_body_omitted: true };
      out = {
        ...m,
        message: {
          ...m.message,
          content: blocks.map((b) => (b.type === "tool_result" && this.openspecCalls.has(b.tool_use_id ?? "") ? { ...b, content: omitted } : b)),
        },
        ...(m.tool_use_result === undefined ? {} : { tool_use_result: omitted }),
      };
    }
    const text = JSON.stringify(out);
    if (text.length <= MAX_EVENT_DATA) return out;
    return { truncated: true, bytes: text.length, type: m.type, preview: text.slice(0, 4096) };
  }
}
