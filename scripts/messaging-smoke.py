#!/usr/bin/env python3
"""Opt-in real Codex: agent question/resume, durable owner inbox, logical owner dialog, with explicit exits.

Runs against a Core serving the smoke company (FACTORY_COMPANY=smoke, configs/smoke/: cheapest models) with
FACTORY_EXPLICIT_EXITS=1 (the default); see docs/development.md, Live smokes. A project without a repository
(scratch: no git facts, no pull request). --only agent,owner,dialog runs a subset (comma list; --restart-core
belongs to owner). Every step stops the script at its first failure (assert or timeout) before another model run
starts.

- agent: demo ends with factory_exit_ask (an ask park, the question goes to its manager); the manager's
  conversation task answers with factory_message_reply and factory_exit_done; demo resumes its session and ends
  with factory_exit_submit; the owner accepts. 2 demo + 1 manager runs.
- owner: demo asks the owner (factory_exit_ask, locked category release, options); the question waits in the
  owner inbox (across a Core restart with --restart-core); the owner answers; demo resumes and submits. 2 demo runs.
- dialog: two owner messages on a manager conversation, each handled by one run of the manager's conversation
  task (factory_message_reply, factory_exit_done), one resumed session. 2 manager runs."""
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

def settled(task_id):
    """The task's runs once none is live (an accepted exit ends the turn; the run ends shortly after)."""
    return wait('/tasks/' + task_id + '/attempts', lambda rs: all(r['status'] == 'ENDED' for r in rs))

def exits(run):
    return [c['tool'] for c in api('GET', '/runs/' + run['spec']['id'] + '/tool-calls') if c['tool'].startswith('factory_exit_') and c['status'] == 200]

def check_runs(runs, want):
    """Every run ENDED succeeded with exactly the expected accepted exit."""
    got = [(r['status'], r.get('outcome'), exits(r)) for r in runs]
    assert got == [('ENDED', 'succeeded', [w]) for w in want], got
    return [{'run': r['spec']['id'], 'session_id': r['session_id'], 'exits': e} for r, (_, _, e) in zip(runs, got)]

def create_task(title, description):
    task = api('POST', '/tasks', {'project_id': project, 'title': title,
        'description': description, 'assignee': 'demo', 'request_id': uuid.uuid4().hex})
    return api('POST', '/tasks/' + task['id'] + '/actions', {'version': task['version'], 'action': 'ready'})

def accept(task):
    task = api('GET', '/tasks/' + task['id'])
    task = api('POST', '/tasks/' + task['id'] + '/review', {'version': task['version'], 'accept': True, 'request_id': uuid.uuid4().hex})
    assert task['status'] == 'DONE', task  # a scratch task: the owner's approval ends it
    return task

preflight('on')
workspace.mkdir(parents=True, exist_ok=True)
api('POST', '/projects', {'id': project, 'name': project, 'workspace': str(workspace)})
summary = {'project': project, 'scenarios': only, 'restart_tested': restart and 'owner' in only}

def manager_conversation():
    """The manager's conversation task of the project (kind conversation; created on first use)."""
    ids = [t['id'] for t in api('GET', '/tasks?project=' + project) if t.get('kind') == 'conversation' and t['assignee'] == 'manager']
    assert len(ids) == 1, ids
    return ids[0]

if 'agent' in only:
    first = create_task('Blocking agent question', '''Use only Factory MCP tools; no shell, no files. If your task brief already contains the answer MESSAGE_AGENT_OK to your question, do not ask again: end the run with factory_exit_submit, summary AGENT_RESUMED_OK, checks ["got the answer"]. Otherwise end the run at once with factory_exit_ask, question "Reply MESSAGE_AGENT_OK with factory_message_reply, then end your run with factory_exit_done. No other work is needed." Do nothing after the exit.''')
    first = wait('/tasks/' + first['id'], lambda t: t['status'] == 'IN_REVIEW')
    assert first['submission']['summary'] == 'AGENT_RESUMED_OK' and first['implementer'] == 'demo', first
    attempts = settled(first['id'])
    demo_runs = check_runs(attempts, ['factory_exit_ask', 'factory_exit_submit'])
    assert attempts[0]['session_id'] and attempts[0]['session_id'] == attempts[1]['session_id'], attempts
    manager_runs = settled(manager_conversation())
    assert manager_runs, 'no manager conversation run'
    check_runs(manager_runs, ['factory_exit_done'] * len(manager_runs))
    replies = [c for r in manager_runs for c in api('GET', '/runs/' + r['spec']['id'] + '/tool-calls') if c['tool'] == 'factory_message_reply' and c['status'] == 200]
    assert replies, 'the manager did not answer through factory_message_reply'
    first = accept(first)
    summary['agent'] = {'task': first['id'], 'status': first['status'], 'runs': demo_runs,
        'manager_runs': [{'run': r['spec']['id'], 'outcome': r['outcome']} for r in manager_runs]}

if 'owner' in only:
    second = create_task('Durable owner question', '''Use only Factory MCP tools; no shell, no files. If your task brief already contains the owner's answer OWNER_ANSWER_OK, do not ask again: end the run with factory_exit_submit, summary OWNER_RESUMED_OK, checks ["got the owner answer"]. Otherwise end the run at once with factory_exit_ask, question "Release audience?", options ["Users","Pros"], category release. Do nothing after the exit.''')
    second = wait('/tasks/' + second['id'], lambda t: t['status'] == 'BLOCKED' and t.get('wait_message_id'))
    question_id = second['wait_message_id']
    check_runs(settled(second['id']), ['factory_exit_ask'])
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
        assert api('GET', '/tasks/' + second['id'])['wait_message_id'] == question_id
    inbox = api('GET', '/inbox?project=' + project)
    question = next(m for m in inbox if m['id'] == question_id)
    assert question['recipient'] == 'owner' and question['message_type'] == 'question', question
    # The options arrive as a single-choice form; a free-text body alone would be accepted too.
    answers = {q['id']: q['options'][-1] for q in question.get('questions') or [] if q['type'] == 'single_choice'}
    api('POST', '/inbox/' + question['id'] + '/answer', {'version': question['version'], 'body': 'OWNER_ANSWER_OK',
        'answers': answers, 'request_id': 'owner-answer-' + project})
    second = wait('/tasks/' + second['id'], lambda t: t['status'] == 'IN_REVIEW')
    assert second['submission']['summary'] == 'OWNER_RESUMED_OK', second
    second_attempts = settled(second['id'])
    owner_runs = check_runs(second_attempts, ['factory_exit_ask', 'factory_exit_submit'])
    assert second_attempts[0]['session_id'] and second_attempts[0]['session_id'] == second_attempts[1]['session_id'], second_attempts
    second = accept(second)
    summary['owner'] = {'task': second['id'], 'status': second['status'], 'question': question_id, 'answers': answers, 'runs': owner_runs}

if 'dialog' in only:
    # The owner's messages are triggers on the manager's conversation task (hierarchy D5): each is handled by a run
    # of that task that ends with factory_exit_done (the task stays open), and its runs resume one session.
    conversation = api('POST', '/conversations', {'project_id': project, 'agent': 'manager',
        'title': 'Persistent owner dialog', 'request_id': 'owner-conversation-' + project})
    earlier = set()
    if 'agent' in only:
        earlier = {r['spec']['id'] for r in settled(manager_conversation())}
    for marker in ('CHAT_ONE_OK', 'CHAT_TWO_OK'):
        message = api('POST', '/conversations/' + conversation['id'] + '/messages', {
            'body': 'Use only Factory MCP. Reply to this question with factory_message_reply, body ' + marker + ', then end the run with factory_exit_done. Do not ask questions or modify files.',
            'request_id': marker + '-' + project})
        message = wait('/messages/' + message['id'], lambda m: m['status'] == 'ANSWERED')
        assert message['delivery_status'] == 'TRIGGERED', message
        task_id = api('GET', '/conversations/' + conversation['id'])['task_id']
        assert task_id, conversation
        settled(task_id)  # the next message starts a new run instead of joining this one
    conversation_runs = [r for r in settled(task_id) if r['spec']['id'] not in earlier]
    dialog_runs = check_runs(conversation_runs, ['factory_exit_done', 'factory_exit_done'])
    assert len({r['session_id'] for r in conversation_runs}) == 1 and conversation_runs[0]['session_id'], conversation_runs
    task = api('GET', '/tasks/' + task_id)
    assert task['status'] not in ('DONE', 'CANCELLED'), task  # done on a conversation task: input handled, it stays open
    history = api('GET', '/conversations/' + conversation['id'] + '/messages')
    answers = [m for m in history if m['message_type'] == 'answer']
    assert len(history) == 4 and len(answers) == 2 and 'CHAT_ONE_OK' in answers[0]['body'] and 'CHAT_TWO_OK' in answers[1]['body'], history
    summary['dialog'] = {'conversation': conversation['id'], 'task': task_id, 'task_status': task['status'],
        'thread': conversation_runs[0]['session_id'], 'runs': dialog_runs, 'messages_in_dialog': len(history)}

Path('.cache/messaging-verification.json').write_text(json.dumps(summary, indent=2) + '\n')
print(json.dumps(summary, indent=2))
