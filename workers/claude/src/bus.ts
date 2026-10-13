// JetStream transport matching internal/bus: same streams, envelopes and dedup message IDs.
import { connect, nanos, type NatsConnection } from "@nats-io/transport-node";
import {
  AckPolicy,
  JetStreamApiCodes,
  JetStreamApiError,
  StorageType,
  jetstream,
  jetstreamManager,
  type Consumer,
  type JetStreamClient,
} from "@nats-io/jetstream";
import { wrap, type EventKind, type RunEvent } from "./protocol.js";

const STREAMS = [
  { name: "FACTORY_COMMANDS", subjects: ["factory.commands.>"] },
  { name: "FACTORY_EVENTS", subjects: ["factory.events.>"] },
];

export class Bus {
  private constructor(readonly nc: NatsConnection, private readonly js: JetStreamClient) {}

  static async connect(url: string): Promise<Bus> {
    // Credentials in the URL are passed explicitly instead of relying on URL parsing.
    const u = new URL(url);
    const nc = await connect({
      servers: `${u.hostname}:${u.port || "4222"}`,
      ...(u.username ? { user: decodeURIComponent(u.username), pass: decodeURIComponent(u.password) } : {}),
      name: "appfactory-claude-worker",
      timeout: 5_000,
      maxReconnectAttempts: -1,
    });
    const jsm = await jetstreamManager(nc);
    for (const s of STREAMS) {
      try {
        await jsm.streams.info(s.name);
      } catch (err) {
        if (!(err instanceof JetStreamApiError && err.code === JetStreamApiCodes.StreamNotFound)) throw err;
        await jsm.streams.add({ ...s, storage: StorageType.File });
      }
    }
    return new Bus(nc, jetstream(nc));
  }

  // One unacknowledged command at a time, like the Go PullSubscribe options.
  async commands(subject: string, durable: string): Promise<Consumer> {
    const jsm = await jetstreamManager(this.nc);
    await jsm.consumers.add("FACTORY_COMMANDS", {
      durable_name: durable,
      filter_subject: subject,
      ack_policy: AckPolicy.Explicit,
      ack_wait: nanos(60_000),
      max_ack_pending: 1,
    });
    return this.js.consumers.get("FACTORY_COMMANDS", durable);
  }

  async event(kind: EventKind, cause: string, payload: RunEvent): Promise<void> {
    const e = wrap(`agent.${kind}`, payload.run_id, cause, payload);
    await this.js.publish(`factory.events.agent.${kind}`, JSON.stringify(e), { msgID: e.event_id, timeout: 10_000 });
  }

  close() {
    return this.nc.close();
  }
}
