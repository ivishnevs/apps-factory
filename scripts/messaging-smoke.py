#!/usr/bin/env python3
"""Opt-in real Codex: agent question/resume, durable owner inbox, logical owner dialog.

Runs against a Core serving the smoke company (FACTORY_COMPANY=smoke, configs/smoke/: cheapest models) with
FACTORY_EXPLICIT_EXITS=0 (the prompts use implicit submission); see docs/development.md, Live smokes.
--only agent,owner,dialog runs a subset (comma list; --restart-core belongs to owner). Every step stops the
script at its first failure (assert or timeout) before another model run starts."""
import json
import os
from pathlib import Path
import subprocess
import sys
import time
import urllib.error
import urllib.request
import uuid

base = os.environ.get('FACTORY_URL', 'http://127.0.0.1:8080')
token = os.environ['FACTORY_TOKEN']
scenarios = ('agent', 'owner', 'dialog')
only = sys.argv[sys.argv.index('--only') + 1].split(',') if '--only' in sys.argv else list(scenarios)
assert only and set(only) <= set(scenarios), 'usage: --only ' + ','.join(scenarios)
restart = '--restart-core' in sys.argv
project = 'messages-smoke-' + uuid.uuid4().hex[:10]
workspace = Path('.cache/' + project).resolve()

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
    raise TimeoutError('Timed out; inspect durable state before retrying: ' + path)

def create_task(title, description):
    task = api('POST', '/tasks', {'project_id': project, 'title': title,
        'description': description, 'assignee': 'demo', 'request_id': uuid.uuid4().hex})
    return api('POST', '/tasks/' + task['id'] + '/actions', {'version': task['version'], 'action': 'ready'})

def accept(task):
    return api('POST', '/tasks/' + task['id'] + '/review', {'version': task['version'], 'accept': True})

preflight('off')
workspace.mkdir(parents=True, exist_ok=True)
api('POST', '/projects', {'id': project, 'name': project, 'workspace': str(workspace)})
summary = {'project': project, 'scenarios': only, 'restart_tested': restart and 'owner' in only}

if 'agent' in only:
    first = create_task('Blocking agent question', '''Use only Factory MCP and never modify files. If no answer to a blocking question is present in your current Factory task prompt, ask manager with factory_message_ask, blocking true, body "Please explicitly answer MESSAGE_AGENT_OK using factory_message_reply. No additional questions or work are needed.", request_id ask-manager. Then immediately finish with WAITING_AGENT. Do not poll or submit. If the current task prompt includes an explicit answer MESSAGE_AGENT_OK, do not ask again; finish exactly AGENT_RESUMED_OK.''')
    first = wait('/tasks/' + first['id'], lambda t: t['status'] == 'IN_REVIEW')
    assert first['attempt'] == 2 and 'AGENT_RESUMED_OK' in first['result'], first
    attempts = api('GET', '/tasks/' + first['id'] + '/attempts')
    assert len(attempts) == 2 and attempts[0]['session_id'] == attempts[1]['session_id'], attempts
    first = accept(first)
    summary['agent'] = {'task': first['id'], 'status': first['status'], 'attempts': first['attempt'], 'thread': attempts[0]['session_id']}

if 'owner' in only:
    second = create_task('Durable owner question', '''Use only Factory MCP and never modify files. If no explicit answer to your blocking question is present in the current task prompt, ask the owner with factory_owner_ask, blocking true, body "Product clarification", request_id ask-owner and these questions: [{"id":"audience","type":"single_choice","question":"Audience?","options":["Users","Pros"]},{"id":"features","type":"multiple_choice","question":"Features?","options":["A","B"]},{"id":"confirmed","type":"confirmation","question":"Confirm?"},{"id":"note","type":"text","question":"Note?"}]. Then finish immediately with WAITING_OWNER. Never poll or submit. When the current task prompt contains an explicit answer OWNER_ANSWER_OK, finish OWNER_RESUMED_OK without asking again.''')
    second = wait('/tasks/' + second['id'], lambda t: t['status'] == 'BLOCKED')
    assert second['wait_message_id'], second
    waiting_run = api('GET', '/runs/' + second['current_run_id'])
    assert waiting_run['status'] == 'COMPLETED', waiting_run
    if restart:
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
    inbox = api('GET', '/inbox?project=' + project)
    question = next(m for m in inbox if m['id'] == second['wait_message_id'])
    api('POST', '/inbox/' + question['id'] + '/answer', {'version': question['version'],
        'body': 'OWNER_ANSWER_OK', 'answers': {'audience': 'Pros', 'features': ['A', 'B'], 'confirmed': True, 'note': 'Approved scope'},
        'request_id': 'owner-answer'})
    second = wait('/tasks/' + second['id'], lambda t: t['status'] == 'IN_REVIEW')
    assert second['attempt'] == 2 and 'OWNER_RESUMED_OK' in second['result'], second
    second_attempts = api('GET', '/tasks/' + second['id'] + '/attempts')
    assert second_attempts[0]['session_id'] == second_attempts[1]['session_id'], second_attempts
    second = accept(second)
    summary['owner'] = {'task': second['id'], 'status': second['status'], 'attempts': second['attempt'], 'thread': second_attempts[0]['session_id']}

if 'dialog' in only:
    # The owner's messages are triggers on the manager's conversation task (hierarchy D5): each is handled by a run
    # of that task, and its runs resume one session.
    conversation = api('POST', '/conversations', {'project_id': project, 'agent': 'manager',
        'title': 'Persistent owner dialog', 'request_id': 'owner-conversation'})
    for marker in ('CHAT_ONE_OK', 'CHAT_TWO_OK'):
        message = api('POST', '/conversations/' + conversation['id'] + '/messages', {
            'body': 'Use only Factory MCP. Explicitly reply to this question using factory_message_reply with body ' + marker + '. Do not ask questions or modify files.',
            'request_id': marker})
        message = wait('/messages/' + message['id'], lambda m: m['status'] == 'ANSWERED')
        assert message['delivery_status'] == 'TRIGGERED', message
    task_id = api('GET', '/conversations/' + conversation['id'])['task_id']
    assert task_id, conversation
    wait('/runs', lambda rs: not any(r['spec'].get('task_id') == task_id and r['status'] in ('QUEUED', 'CLAIMED', 'RUNNING', 'CANCELLING', 'UNKNOWN') for r in rs))
    conversation_runs = api('GET', '/tasks/' + task_id + '/attempts')
    assert len(conversation_runs) >= 2 and len({r['session_id'] for r in conversation_runs}) == 1 and conversation_runs[0]['session_id'], conversation_runs
    history = api('GET', '/conversations/' + conversation['id'] + '/messages')
    answers = [m for m in history if m['message_type'] == 'answer']
    assert len(history) == 4 and len(answers) == 2 and 'CHAT_ONE_OK' in answers[0]['body'] and 'CHAT_TWO_OK' in answers[1]['body'], history
    summary['dialog'] = {'conversation': conversation['id'], 'task': task_id, 'thread': conversation_runs[0]['session_id'],
        'runs': [r['spec']['id'] for r in conversation_runs], 'messages_in_dialog': len(history)}

Path('.cache/messaging-verification.json').write_text(json.dumps(summary, indent=2))
print(json.dumps(summary, indent=2))
