// Entry of the single-executable shim (node-packaging-updates 2.1): no Node.js on the machine.
import { main } from "./shim.js";

main().catch((err: unknown) => {
  process.stderr.write(`shim: ${String(err)}\n`);
  process.exit(1);
});
