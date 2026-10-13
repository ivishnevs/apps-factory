// Runtime environment shared by the standalone Worker and the per-run shim. The runtime receives
// only a run-scoped Factory credential. Host access remains trusted.
const OWNER_SECRETS = ["FACTORY_TOKEN", "NATS_URL", "NATS_PASSWORD"];
const API_KEYS = ["ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN"];

const RUNTIME_ENV = {
  CLAUDE_AGENT_SDK_CLIENT_APP: "appfactory-claude-worker/0.1.0",
  // A turn is the unit of work: background shells would outlive it and die with the runtime.
  CLAUDE_CODE_DISABLE_BACKGROUND_TASKS: "1",
};

function scrub(env: NodeJS.ProcessEnv, drop: string[], allowApiKey: boolean): Record<string, string | undefined> {
  const out: Record<string, string | undefined> = {};
  for (const [k, v] of Object.entries(env)) {
    if (drop.includes(k) || (!allowApiKey && API_KEYS.includes(k))) continue;
    out[k] = v;
  }
  return out;
}

// Standalone Worker: replaces the owner credential with the run credential fetched from Core.
export function runEnvironment(env: NodeJS.ProcessEnv, base: string, token: string, allowApiKey = false): Record<string, string | undefined> {
  return {
    ...scrub(env, [...OWNER_SECRETS, "FACTORY_RUN_TOKEN", "FACTORY_URL"], allowApiKey),
    FACTORY_URL: base,
    FACTORY_RUN_TOKEN: token,
    ...RUNTIME_ENV,
  };
}

// Shim: the driver already put FACTORY_URL and FACTORY_RUN_TOKEN into the shim's environment;
// owner secrets are dropped defensively in case the driver leaked them.
export function shimEnvironment(env: NodeJS.ProcessEnv, allowApiKey = false): Record<string, string | undefined> {
  return { ...scrub(env, OWNER_SECRETS, allowApiKey), ...RUNTIME_ENV };
}
