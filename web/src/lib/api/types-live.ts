// Shapes the live screens (Run, Activity, Inbox) derive or read beyond types.ts.

// TxEntry is one entry of a run transcript (internal/tui/transcript_parse.go TxEntry).
export type TxKind = "say" | "think" | "tool" | "refusal" | "error" | "info" | "end";
export interface TxEntry {
  key: string;
  at: number; // ms since epoch
  kind: TxKind;
  // tool: the tool's name without the MCP prefix; id pairs a call with its result.
  tool?: string;
  id?: string;
  // input: full input (a shell command, indented JSON otherwise); brief: its one-line form.
  input?: string;
  brief?: string;
  result?: string;
  done?: boolean;
  isError?: boolean;
  text?: string;
  // outcome of an end entry.
  outcome?: string;
}

// Fields the API sends that types.ts does not mirror yet.
export interface ArtifactMeta { sha256?: string }
export interface UsageMeta { cost_source?: string }
