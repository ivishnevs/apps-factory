// Progress event payloads: oversized messages are bounded below broker limits.
export const MAX_EVENT_DATA = 256 * 1024;

export class Redactor {
  data(message: unknown): unknown {
    const m = message as { type?: string };
    const text = JSON.stringify(message);
    if (text.length <= MAX_EVENT_DATA) return message;
    return { truncated: true, bytes: text.length, type: m.type, preview: text.slice(0, 4096) };
  }
}
