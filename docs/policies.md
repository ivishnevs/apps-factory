# Unified policies

One catalog in configs/policies/*.yaml contains access, escalation and reporting instructions.
Use apiVersion factory/v1, kind Policy, metadata.name and spec:

```yaml
spec:
  extends: optional-parent
  mandatory: true
  rules:
    - name: protect-release
      type: access
      operations: [factory.release.publish]
      effect: approval
      instruction: Ask the owner before publishing a concrete app version.
    - name: product-question
      type: escalation
      roles: [developer]
      target: manager
      instruction: Escalate unresolved product decisions to the manager.
    - name: report-dependencies
      type: reporting
      instruction: Report dependency installation, its purpose and result.
```

Agents attach policies by name. All mandatory policies also apply automatically.
Parent rules are additive and cannot be overridden with the same rule name; duplicate names in a
resolved policy fail validation. A mandatory parent remains mandatory when a child says false.
Identical inherited rules selected through multiple policies are rendered once.
Optional roles/agents filters match the agent role/name; both filters must match when both exist.

Access decisions combine tool filters and every applicable rule: deny > approval > allow.
An allow rule cannot override a denylist or grant a tool absent from an allowlist. For escalation,
target is self/manager/owner; instructions govern the trusted agent. Reporting instructions describe
significant events; agents persist these through factory_report_create. Reporting is part of the
same policy system, independent of access approval and escalation.
Behavioral instructions with contradictory escalation/reporting rules are not automatically
arbitrated; configure consistent instructions. Access conflicts have the precedence above.

The reserved immutable `system` policy requires owner approval for project deletion, release
publication, production credentials, paid external purchases and factory configuration changes.
It cannot be replaced by YAML, removed from an agent or bypassed using unrestricted/never.
The protected operation names are factory.project.delete, factory.release.publish,
factory.secrets.access_production, external.purchase, factory.config.* and factory.decision.record.

`factory agents authorize AGENT OPERATION` previews the decision without executing an operation.
The API exposes POST /agents/{agent}/authorize with {"operation":"..."}. This is an owner-level
inspection endpoint, not an agent capability grant. Actual skill reads/lists/searches enforce
the evaluator: deny -> 403, approval -> 423. No boolean approval bypass is accepted. Future tools
must use the same evaluator and bind approval to concrete parameters and artifact versions.

Full host access means prompt rules and Factory tool filtering are not strict OS isolation.
The shared Core token identifies trusted service callers; per-agent credentials remain future work.
