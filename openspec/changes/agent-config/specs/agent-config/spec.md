# Requirements

## Configuration
Profiles SHALL merge with explicit child scalar precedence; inherited skills and policies SHALL
be additive. Cycles, missing references, unknown fields and unsupported limits SHALL fail loading.

## Skills
Only metadata SHALL be included at startup. Reads SHALL be scoped to the selected agent's explicit
skills or enabled discovery categories and confined to the configured skill directory.

## Policies
One catalog SHALL contain access, escalation and reporting rules. Mandatory rules SHALL survive
inheritance and unrestricted/never agent settings. Deny SHALL take precedence over approval/allow.
Core skill operations SHALL check resolved access decisions before reading resource content.
