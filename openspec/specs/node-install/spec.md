# node-install Specification

## Purpose
Let a customer install, connect and start a node on their own machine with one command, from packages and a script
that Core hands out for its own version.

## Requirements

### Requirement: Node packages handed out by Core
Core SHALL hand out, without a credential, an install script and the node packages of its own version for macOS and
Linux on arm64 and amd64, with their SHA-256 sums.

#### Scenario: Downloading a package
- **WHEN** a machine requests `/downloads/node/factory-node-darwin-arm64.tar.gz` and `SHA256SUMS`
- **THEN** Core answers the package and its sum without a credential

#### Scenario: Install script names this Core
- **WHEN** a machine requests `/install.sh`
- **THEN** the script it receives downloads from and connects to this Core's node address

### Requirement: One-command install
The install script SHALL install or upgrade the node in the user's home, check the sum of the package, install the
Claude shim's pinned dependencies when Node.js 22+ is present, write `node.yaml` once from what it finds, connect the
node unless it already belongs to a company, and start it as a user service.

#### Scenario: Fresh machine with a browser
- **WHEN** a person runs the install command on a Mac with `git`, Node.js and Codex
- **THEN** the node is installed, the connection page opens, and after approval the node runs as a launchd agent

#### Scenario: Re-running the script
- **WHEN** the script runs on a machine whose node is already connected
- **THEN** the binaries are replaced, `node.yaml` and the keys are kept, no new connection is asked and the service
  restarts
