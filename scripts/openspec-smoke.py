#!/usr/bin/env python3
"""Opt-in real Codex: OpenSpec in the project repository (openspec-in-git), with explicit exits.

Runs against a Core serving the smoke company (FACTORY_COMPANY=smoke, configs/smoke/: cheapest models) with
FACTORY_EXPLICIT_EXITS=1 (the default) and a node on this machine; see docs/development.md, Live smokes. The
project is an existing local git repository under .cache (no remote: tasks share its workspace on main, and
done needs a clean tree with nothing ahead of main).

Two model runs:
1. Proposal (manager): writes change add-greeting with the official CLI, validates it, commits it on main,
   creates the linked executor task depending on itself (marked as the last task of the change) and exits done.
2. Implementation (demo): writes greeting.txt, checks its tasks.md items, archives the change
   (openspec archive), commits and exits done; its done facts report the change archived.
The executor closing usually wakes the manager's conversation (delegated_closed), one more short run.

Then, without a model run: a task linked to a change that is not in the repository ends its run
change_missing before the runtime starts, opens a change_missing escalation and is cancelled.
The first failed step stops the script."""
import json
import os
from pathlib import Path
import subprocess
import time
import urllib.error
import urllib.request
import uuid

base = os.environ.get('FACTORY_URL', 'http://127.0.0.1:8080')
token = os.environ['FACTORY_TOKEN']
project = 'openspec-smoke-' + uuid.uuid4().hex[:10]
repo = Path('.cache/' + project).resolve()
change = 'add-greeting'


def api(method, path, body=None):
    req = urllib.request.Request(base + path, method=method,
        data=json.dumps(body).encode() if body is not None else None,
        headers={'Authorization': 'Bearer ' + token, 'Content-Type': 'application/json'})
    with urllib.request.urlopen(req, timeout=15) as response:
        return json.load(response)


def preflight():
    """Refuses to start (no model run) unless Core serves the smoke company with explicit exits."""
    health = api('GET', '/health')
    company = os.environ.get('SMOKE_COMPANY', 'smoke')
    assert company == 'any' or health.get('company') == company, f"Core serves company {health.get('company')!r}, not {company!r}: start it with FACTORY_COMPANY={company} (or SMOKE_COMPANY=any)"
    assert health.get('explicit_exits') == 'on', f"Core explicit_exits={health.get('explicit_exits')!r}; this smoke needs FACTORY_EXPLICIT_EXITS=1"
    for tool in ('git', 'openspec'):
        subprocess.run([tool, '--version'], check=True, capture_output=True)


def wait(path, predicate, timeout=330):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        value = api('GET', path)
        if predicate(value):
            return value
        if isinstance(value, dict):
            assert value.get('status') not in ('FAILED', 'CANCELLED', 'UNKNOWN'), value
        time.sleep(.5)
    raise TimeoutError('Inspect durable state before retrying: ' + path)


def git(*args):
    return subprocess.run(['git', *args], cwd=repo, check=True, capture_output=True, text=True).stdout.strip()


def create_task(agent, title, prompt, link=''):
    body = {'project_id': project, 'title': title, 'description': prompt, 'assignee': agent, 'request_id': project + '-' + title}
    if link:
        body['openspec_change'] = link
    t = api('GET', '/tasks/' + api('POST', '/tasks', body)['id'])
    if t['status'] != 'BACKLOG':
        return t
    return api('POST', '/tasks/' + t['id'] + '/actions', {'version': t['version'], 'action': 'ready'})


def exits(task_id):
    return api('GET', '/tasks/' + task_id + '/exits')


def feed_types():
    return [i['type'] for i in api('GET', '/projects/' + project + '/feed?limit=100')['items']]


preflight()

# An existing repository that tracks a minimal openspec/ tree; nothing is pushed anywhere.
for d in ('specs', 'changes'):
    (repo / 'openspec' / d).mkdir(parents=True)
    (repo / 'openspec' / d / '.gitkeep').write_text('')
(repo / 'openspec/config.yaml').write_text('schema: spec-driven\n')
(repo / '.gitignore').write_text('/.factory/\n')
(repo / 'README.md').write_text('# OpenSpec smoke\n')
subprocess.run(['git', 'init', '-q', '-b', 'main', str(repo)], check=True)
git('add', '.')
git('-c', 'user.name=smoke', '-c', 'user.email=smoke@example.com', 'commit', '-q', '-m', 'init')
start = git('rev-parse', 'HEAD')
api('POST', '/projects', {'id': project, 'name': project, 'repo_mode': 'existing', 'workspace': str(repo), 'request_id': project})

# Core validates the change name only.
try:
    api('POST', '/tasks', {'project_id': project, 'title': 'Invalid link', 'openspec_change': 'Not A Name', 'request_id': project + '-invalid-link'})
except urllib.error.HTTPError as error:
    assert error.code == 400, error
else:
    raise AssertionError('Invalid change name accepted')

plan = create_task('manager', 'OPENSPEC_PLAN', ('This is a bounded OpenSpec scenario. Open the shared openspec skill from your runtime's skills. '
    'OpenSpec lives in this repository under openspec/ (already initialized). Create exactly one change add-greeting with the installed official openspec CLI '
    '(inspect openspec new change --help first). Author proposal.md (sections Why, What Changes, Capabilities with New Capabilities greeting, Impact), '
    'specs/greeting/spec.md (## ADDED Requirements, ### Requirement: Greeting file, a SHALL requirement that greeting.txt equals HELLO_OPENSPEC followed by one newline, '
    'and a #### Scenario with WHEN/THEN bullets) and tasks.md with one unchecked implementation item and one unchecked verification item. Keep it tiny. '
    'Run openspec validate add-greeting --strict --no-interactive until it passes, then commit the change on the current branch (main) with git, leaving a clean tree. '
    'Your own task id is on the Task line of your brief. Create exactly one executor task with factory_task_create: title OPENSPEC_IMPLEMENT, assignee demo, priority 10, '
    'openspec_change add-greeting, dependencies [your task id], request_id executor-PROJECT. Its description must say: this is the last task of change add-greeting; '
    'read openspec/changes/add-greeting (proposal, tasks, spec) in the checkout; write greeting.txt with exactly HELLO_OPENSPEC plus a newline and check the bytes with Python; '
    'mark both tasks.md items done; archive the change with openspec archive add-greeting --yes; commit everything on main with git (clean tree); '
    'end the run with factory_exit_done, summary OPENSPEC_IMPLEMENT_OK. Make the executor ready (factory_task_ready with its current version and a stable request_id). '
    'Do not wait for it and do not review work. End your run with factory_exit_done, summary OPENSPEC_PLAN_OK.').replace('PROJECT', project))

plan = wait('/tasks/' + plan['id'], lambda t: t['status'] == 'DONE')
assert 'OPENSPEC_PLAN_OK' in plan['result'], plan
assert '## Why' in git('show', 'main:openspec/changes/add-greeting/proposal.md')
children = [t for t in api('GET', '/tasks?project=' + project) if t['title'] == 'OPENSPEC_IMPLEMENT']
assert len(children) == 1 and children[0]['openspec_change'] == change and plan['id'] in children[0]['dependencies'], children
plan_exit = exits(plan['id'])[-1]
assert plan_exit['kind'] == 'done' and plan_exit['facts']['ahead_of_default'] == 0 and not plan_exit['facts']['dirty'], plan_exit

executor = wait('/tasks/' + children[0]['id'], lambda t: t['status'] == 'DONE')
assert 'OPENSPEC_IMPLEMENT_OK' in executor['result'], executor
assert (repo / 'greeting.txt').read_bytes() == b'HELLO_OPENSPEC\n'
tree = git('ls-tree', '-r', '--name-only', 'main', 'openspec').splitlines()
archived = [p for p in tree if p.startswith('openspec/changes/archive/') and p.endswith('-add-greeting/proposal.md')]
assert len(archived) == 1 and 'openspec/changes/add-greeting/proposal.md' not in tree and 'openspec/specs/greeting/spec.md' in tree, tree
assert '- [ ]' not in git('show', 'main:' + archived[0].replace('proposal.md', 'tasks.md'))
done = exits(executor['id'])[-1]
assert done['kind'] == 'done' and done['facts']['openspec'] == {'change': change, 'state': 'archived', 'unchecked': 0, 'ref': 'main'}, done
types = feed_types()
assert not any(t in types for t in ('openspec.change_unarchived', 'openspec.change_unfinished')), types

# A change that is not in the repository: change_missing before the runtime, an escalation, no attempt.
missing = create_task('demo', 'OPENSPEC_MISSING', 'Never runs: its change is not in the repository.', link='not-merged-yet')
run = wait('/tasks/' + missing['id'] + '/attempts', lambda rs: bool(rs) and rs[-1]['status'] == 'ENDED')[-1]
assert run['outcome'] == 'change_missing' and "is not on the task's checkout" in run['result'], run
assert not api('GET', '/runs/' + run['spec']['id'] + '/tool-calls'), 'the runtime started'
escalations = wait('/escalations', lambda es: any(e['task_id'] == missing['id'] and e['reason'] == 'change_missing' for e in es))
escalations = [e for e in escalations if e['task_id'] == missing['id'] and e['reason'] == 'change_missing']
assert len(escalations) == 1 and escalations[0]['answers'] == ['retry', 'cancel'], escalations
held = api('GET', '/tasks/' + missing['id'])
assert not held.get('attempts_no_progress') and not held.get('not_before'), held
api('POST', '/tasks/' + missing['id'] + '/actions', {'version': held['version'], 'action': 'cancel', 'reason': 'smoke stays bounded', 'request_id': project + '-cancel-missing'})

wait('/runs', lambda rs: not any(r['spec']['project'] == project and r['status'] != 'ENDED' for r in rs))
summary = {'project': project, 'repository': str(repo), 'change': change, 'initial_commit': start, 'main': git('rev-parse', 'main'),
    'tasks': {'plan': plan['id'], 'executor': executor['id'], 'missing': missing['id']}, 'archived': archived[0],
    'done_facts': done['facts']['openspec'], 'change_missing_run': run['spec']['id'], 'escalation': escalations[0]['id'],
    'notices': [t for t in types if t.startswith('openspec.')]}
Path('.cache/openspec-verification.json').write_text(json.dumps(summary, indent=2) + '\n')
print(json.dumps(summary, indent=2))
