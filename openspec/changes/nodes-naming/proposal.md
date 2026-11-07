Status: decided 2026-11-06 by the owner ("not connectors but nodes"); ready to implement. Nothing implemented.

## Why

The product calls a machine that runs agents a "connector" in the web (Connectors page, "connect a machine"), while
the binary, the installer, the CLI, the docs and the code call it a node (`factory-node`, `node.yaml`, "nodes" API).
Two words for one thing; and the coming hosted machines are "virtual nodes" (`hosted-connectors`).

## What Changes

- The web, Telegram, emails and docs say **node** everywhere: the Nodes page, "Add a node", "your nodes", node
  status and errors. Hosted ones will be **virtual nodes**.
- Routes in the web move from `/connectors` to `/nodes` with a redirect; the API and CLI already say node.
- Plan limits are "nodes of your own" (`saas-billing`).
- Change names in OpenSpec stay as they are (`connector-trust`, `hosted-connectors`); their text uses node.

## Impact

Web (navigation, pages, copy, routes with redirects), landing and docs copy, Telegram texts, `owner-web` spec wording.

## Implementation (2026-11-07)

Web copy (navigation, pages, buttons, empty states, reasons, feed, landing), the Telegram digest and the pause scope
say "node"; `/c/{company}/connectors` redirects to `/nodes`; a test over the web sources fails on "connector" outside
comments. Code identifiers (`InstallConnector`) and developer docs keep their names.
