#!/usr/bin/env python3
"""Opt-in real Codex: Manager authors OpenSpec -> linked Developer -> Review, with explicit exits.

The OpenSpec tree lives in Core (core-owned-project-data): the node materializes it into the run's
workspace and uploads the run's edits at its exit (compare-and-set against the run's manifest). This script
runs on the node's machine, reads the tree only through the owner API and validates a downloaded copy with the
official CLI.

Runs against a Core serving the smoke company (FACTORY_COMPANY=smoke, configs/smoke/: cheapest models) with
FACTORY_EXPLICIT_EXITS=1 (the default); see docs/development.md, Live smokes. A project without a repository
(scratch). Three model runs: the plan (manager, factory_openspec_sync, then factory_exit_done), the linked
implementation (demo, factory_exit_submit to the owner's review) and the linked review (manager: checks the
change off in its worktree, factory_exit_done uploads the edit); the owner then accepts the implementation, and
Core's archive task is cancelled. The executor closing also wakes the manager's conversation (delegated_closed),
usually one more short run. The first failed step stops the script; --resume PROJECT continues after the plan and
implementation."""
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid

base = os.environ.get('FACTORY_URL', 'http://127.0.0.1:8080')
token = os.environ['FACTORY_TOKEN']
resume = sys.argv[sys.argv.index('--resume') + 1] if '--resume' in sys.argv else None
project = resume or ('openspec-smoke-' + uuid.uuid4().hex[:10])
progress_path = Path('.cache/' + project + '-verification-progress.json')
workspace = Path('.cache/' + project).resolve()
change = 'add-greeting'
root = '/projects/' + project

def api(method, path, body=None):
    req = urllib.request.Request(base + path, method=method,
        data=json.dumps(body).encode() if body is not None else None,
        headers={'Authorization': 'Bearer ' + token, 'Content-Type': 'application/json'})
    with urllib.request.urlopen(req, timeout=15) as response:
        return json.load(response)

def preflight(exits):
    """Refuses to start (no model run) unless Core serves the smoke company with the expected exits switch."""
    health = api('GET', '/health')
    company = os.environ.get('SMOKE_COMPANY', 'smoke')
    assert company == 'any' or health.get('company') == company, f"Core serves company {health.get('company')!r}, not {company!r}: start it with FACTORY_COMPANY={company} (or SMOKE_COMPANY=any)"
    assert health.get('explicit_exits') == exits, f"Core explicit_exits={health.get('explicit_exits')!r}; this smoke needs FACTORY_EXPLICIT_EXITS={'1' if exits == 'on' else '0'}"

def wait(path, predicate):
    deadline = time.monotonic() + 330
    while time.monotonic() < deadline:
        value = api('GET', path)
        if predicate(value):
            return value
        if isinstance(value, dict):
            assert value.get('status') not in ('FAILED', 'CANCELLED', 'UNKNOWN'), value
        time.sleep(.5)
    raise TimeoutError('Inspect durable state before retrying: ' + path)

def create_task(agent, title, prompt, linked=False):
    body = {'project_id': project, 'title': title, 'description': prompt, 'assignee': agent, 'request_id': project + '-' + title}
    if linked:
        body['openspec_change'] = change
    t = api('POST', '/tasks', body)
    t = api('GET', '/tasks/' + t['id'])
    if t['status'] != 'BACKLOG':
        return t
    return api('POST', '/tasks/' + t['id'] + '/actions', {'version': t['version'], 'action': 'ready'})

def download_tree(dest):
    """Writes Core's current OpenSpec tree below dest/openspec (for the official CLI)."""
    for doc in api('GET', root + '/openspec/manifest'):
        content, offset, digest = '', 0, ''
        while True:
            query = {'path': doc['path'], 'offset': offset, 'limit': 32768}
            if offset:
                query['sha256'] = digest
            page = api('GET', root + '/openspec/read?' + urllib.parse.urlencode(query))
            content, offset, digest = content + page['content'], page['next_offset'], page['sha256']
            if not page['has_more']:
                break
        target = dest / 'openspec' / doc['path']
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(content)
        assert hashlib.sha256(target.read_bytes()).hexdigest() == doc['sha256'], doc

def accept(t):
    t = api('GET', '/tasks/' + t['id'])
    if t['status'] == 'DONE':
        return t
    t = api('POST', '/tasks/' + t['id'] + '/review', {'version': t['version'], 'accept': True, 'request_id': project + '-accept-' + t['id']})
    assert t['status'] == 'DONE', t  # a scratch task: the owner's approval ends it
    return t

def settled(task_id):
    """The task's runs once none is live (an accepted exit ends the turn; the run ends shortly after)."""
    return wait('/tasks/' + task_id + '/attempts', lambda rs: all(r['status'] == 'ENDED' for r in rs))

def exits(run_id):
    return [c['tool'] for c in api('GET', '/runs/' + run_id + '/tool-calls') if c['tool'].startswith('factory_exit_') and c['status'] == 200]

preflight('on')
workspace.mkdir(parents=True, exist_ok=True)
if not resume:
    api('POST', '/projects', {'id': project, 'name': project, 'workspace': str(workspace)})
    init_body = {'request_id': 'bootstrap'}
    initialized = api('POST', root + '/openspec/init', init_body)
    assert api('POST', root + '/openspec/init', init_body) == initialized
    assert initialized['directories'][-1] == 'openspec/changes/archive', initialized
    try:
        api('POST', '/tasks', {'project_id': project, 'title': 'Invalid link', 'openspec_change': 'missing', 'request_id': 'invalid-link'})
    except urllib.error.HTTPError as error:
        assert error.code == 400, error
    else:
        raise AssertionError('Missing change reference accepted')
    plan_prompt = '''This is a bounded OpenSpec integration scenario. Read the shared openspec skill via factory_skills_read and list this project's OpenSpec tree via factory_openspec_list. Create exactly one active change add-greeting using the installed official openspec CLI (inspect new change help first). Author proposal.md, design.md, tasks.md and specs/greeting/spec.md under that change using normal file tools. Do not modify global config. Keep it tiny and valid for the spec-driven schema: proposal sections Why, What Changes, Capabilities (New Capabilities greeting), Impact; delta spec ## ADDED Requirements, ### Requirement: Greeting file, a SHALL requirement that greeting.txt equals HELLO_OPENSPEC followed by one newline, and a #### Scenario with WHEN/THEN bullets. tasks.md should contain one unchecked implementation task and one unchecked verification task. No base spec needs to be created before archive. Upload your edits with factory_openspec_sync (the linked task below needs the proposal in Factory), then use factory_openspec_check for add-greeting and factory_openspec_read to read your proposal. Create exactly one executor task titled OPENSPEC_IMPLEMENT, assignee demo, priority 10, openspec_change add-greeting, request_id executor-PROJECT. Its description must require reading proposal, tasks and the delta spec through Factory MCP, writing greeting.txt with exactly HELLO_OPENSPEC plus newline, checking exact bytes with Python, and ending the run with factory_exit_submit, summary OPENSPEC_IMPLEMENT_OK, no reviewer. Make the executor ready with its current version and a stable request_id. Do not wait for it, do not review work and do not delegate outside Factory. Create a stage_completed report for preparing the change and linked task, request_id plan-report, then end the run with factory_exit_done, summary OPENSPEC_PLAN_OK.'''.replace('PROJECT', project)
    plan = create_task('manager', 'OPENSPEC_PLAN', plan_prompt)
    plan = wait('/tasks/' + plan['id'], lambda t: t['status'] == 'DONE')
    assert 'OPENSPEC_PLAN_OK' in plan['result'], plan
    children = [t for t in api('GET', '/tasks?project=' + project) if t['title'] == 'OPENSPEC_IMPLEMENT']
    assert len(children) == 1 and children[0]['openspec_change'] == change, children
    executor = wait('/tasks/' + children[0]['id'], lambda t: t['status'] == 'IN_REVIEW')
    assert executor['submission']['summary'] == 'OPENSPEC_IMPLEMENT_OK' and executor['implementer'] == 'demo', executor
    settled(executor['id'])
    assert (workspace / 'greeting.txt').read_bytes() == b'HELLO_OPENSPEC\n'
    assert api('GET', root + '/openspec/check/' + change)['valid']
    assert api('GET', '/tasks/' + executor['id'])['status'] == 'IN_REVIEW'
    # Core's copy is the source across restart and subsequent edits.
    read_path = root + '/openspec/read?' + urllib.parse.urlencode({'path': 'changes/add-greeting/proposal.md'})
    old = api('GET', read_path)
    progress = {'project': project, 'plan': plan, 'executor': executor, 'old': old, 'restarted': False}
    progress_path.write_text(json.dumps(progress, indent=2) + '\n')
else:
    progress = json.loads(progress_path.read_text())
    assert progress['project'] == project
    plan, executor, old = progress['plan'], progress['executor'], progress['old']
    read_path = root + '/openspec/read?' + urllib.parse.urlencode({'path': 'changes/add-greeting/proposal.md'})
if '--restart-core' in sys.argv and not progress['restarted']:
    subprocess.run(['docker', 'compose', 'restart', 'core'], check=True)
    deadline = time.monotonic() + 30
    while True:
        try:
            api('GET', '/health')
            break
        except (urllib.error.URLError, ConnectionError):
            if time.monotonic() > deadline:
                raise
            time.sleep(.5)
    assert api('GET', read_path) == old
    progress['restarted'] = True
    progress_path.write_text(json.dumps(progress, indent=2) + '\n')
# The owner edits the proposal in Core with compare-and-set; a write from the old base conflicts.
note = '\nReview note: verify the exact bytes before accepting the Factory task.\n'
current = api('GET', read_path + '&limit=32768')
if note not in current['content']:
    assert not current['has_more'], 'proposal larger than one page'
    api('POST', root + '/openspec/write', {'path': 'changes/add-greeting/proposal.md', 'base_sha256': current['sha256'], 'content': current['content'] + note})
new = api('GET', read_path + '&limit=32768')
assert old['sha256'] != new['sha256'] and 'Review note:' in new['content']
try:
    api('POST', root + '/openspec/write', {'path': 'changes/add-greeting/proposal.md', 'base_sha256': old['sha256'], 'content': 'stale'})
except urllib.error.HTTPError as error:
    assert error.code == 409 and json.load(error)['current_sha256'] == new['sha256'], error
else:
    raise AssertionError('Write from a stale base accepted')
try:
    api('GET', read_path + '&' + urllib.parse.urlencode({'sha256': old['sha256']}))
except urllib.error.HTTPError as error:
    assert error.code == 409, error
else:
    raise AssertionError('Changed document accepted with stale digest')
review = create_task('manager', 'OPENSPEC_REVIEW', '''This is a bounded OpenSpec review. Read current proposal, tasks and delta spec in your worktree's openspec/ directory (or via factory_openspec_read). The proposal must include the current "Review note:" added after implementation. Verify greeting.txt is exactly HELLO_OPENSPEC followed by one newline, using Python. Run official openspec validate add-greeting --strict --json --no-interactive; inspect help if needed. Only after verification passes, mark both OpenSpec tasks complete using file tools (your exit uploads the edit). Read Factory executor TASK_ID via factory_task_get and verify it is still IN_REVIEW despite checked Markdown tasks. Do not review or change that task: the owner accepts it. Create a stage_completed report for the verified change, request_id review-report. Do not archive the change yet, do not create new tasks or delegate. End the run with factory_exit_done, summary OPENSPEC_REVIEW_OK (the exit uploads your tasks.md edit).'''.replace('TASK_ID', executor['id']), linked=True)
review = wait('/tasks/' + review['id'], lambda t: t['status'] == 'DONE')
assert 'OPENSPEC_REVIEW_OK' in review['result'], review
assert api('GET', '/tasks/' + executor['id'])['status'] == 'IN_REVIEW'
executor = accept(executor)
# Every task linked to the change is DONE: Core created exactly one archive task (for the manager). It is
# cancelled at once to keep the scenario bounded (archiving is a separate agent run).
archive = [t for t in api('GET', '/tasks?project=' + project) if t['title'] == 'Archive OpenSpec change ' + change]
assert len(archive) == 1 and archive[0]['openspec_change'] == change, archive
for _ in range(5):  # the scheduler may claim it meanwhile (a new version): cancel the latest one
    current = api('GET', '/tasks/' + archive[0]['id'])
    if current['status'] in ('DONE', 'CANCELLED') or current.get('cancel_requested'):
        break
    try:
        api('POST', '/tasks/' + current['id'] + '/actions', {'version': current['version'], 'action': 'cancel', 'reason': 'smoke stays bounded', 'request_id': project + '-cancel-archive-' + str(current['version'])})
        break
    except urllib.error.HTTPError as error:
        assert error.code == 409, error
tree = Path('.cache/' + project + '-tree').resolve()
download_tree(tree)
assert '- [x]' in (tree / 'openspec/changes/add-greeting/tasks.md').read_text(), 'review edits not uploaded at its exit'
validation = subprocess.run(['openspec', 'validate', change, '--strict', '--json', '--no-interactive'], cwd=tree, capture_output=True, text=True, check=True)
assert json.loads(validation.stdout)['summary']['totals']['failed'] == 0
wait('/runs', lambda rs: not any(r['spec']['project'] == project and r['status'] != 'ENDED' for r in rs))
attempts = [api('GET', '/tasks/' + t['id'] + '/attempts')[-1] for t in (plan, executor, review)]
assert attempts[1]['spec']['openspec_change'] == change and attempts[2]['spec']['openspec_change'] == change
run_exits = [(a['status'], a['outcome'], exits(a['spec']['id'])) for a in attempts]
assert run_exits == [('ENDED', 'succeeded', [e]) for e in ('factory_exit_done', 'factory_exit_submit', 'factory_exit_done')], run_exits
audits = []
read_bodies_omitted = 0
for attempt in attempts:
    rid = attempt['spec']['id']
    calls = api('GET', '/runs/' + rid + '/tool-calls')
    # Writes and syncs must succeed; a read or list of a path the agent guessed may answer 404.
    reads = ('factory_openspec_read', 'factory_openspec_list')
    assert all(c['status'] == 200 or (c['tool'] in reads and c['status'] == 404) for c in calls if c['tool'].startswith('factory_openspec_')), calls
    assert all(0 < c['status'] < 500 for c in calls), calls
    audits.append({'run_id': rid, 'tools': [{'tool': c['tool'], 'status': c['status']} for c in calls]})
    events = api('GET', '/runs/' + rid + '/events')
    read_bodies_omitted += json.dumps(events).count('document_body_omitted')
assert read_bodies_omitted > 0
assert not api('GET', '/inbox?project=' + project)
summary = {'project': project, 'workspace': str(workspace), 'change': change, 'tasks': [t['id'] for t in (plan, executor, review)],
    'runs': [{'run': a['spec']['id'], 'agent': a['spec']['agent'], 'outcome': a['outcome'], 'exits': e} for a, (_, _, e) in zip(attempts, run_exits)], 'proposal_sha256_before': old['sha256'], 'proposal_sha256_after': new['sha256'],
    'archive_task': archive[0]['id'], 'all_tasks_done': True, 'semantic_validation_passed': True, 'read_bodies_omitted': read_bodies_omitted, 'tool_audit': audits, 'inbox_empty': True}
Path('.cache/openspec-verification.json').write_text(json.dumps(summary, indent=2) + '\n')
print(json.dumps(summary, indent=2))
