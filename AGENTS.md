# Rules for agents working on App Factory

## Changes are systemic

Every change must work for every user and every supported configuration of a company, project, team and agent,
not for the setup of one particular company.

- Fix the class of the problem, not the instance. A bug seen in one company (a test company, the owner's own) is
  fixed in Core, the node, the web or the defaults so that every company with that configuration gets the fix.
- Never hard-code a company, project, team, agent, task or person name, id or prompt in code, migrations, defaults
  or tests that stand for general behaviour.
- Consider the whole configuration space the change touches: single and multi company; projects with and without a
  team or repository; teams with one or several leads; agents with different roles, runtimes, permissions and
  delegation settings; people with different roles; nodes of different versions. Handle each or state the limit.
- Data repairs of one company (answering its escalations, editing its tasks) are not fixes. Make the system fix it,
  or propose the systemic fix and do the one-off repair only when the owner asks for it.
- Tests describe the general rule with fixtures, then may replay a real case; a test that only passes for one
  company's data is not enough.
