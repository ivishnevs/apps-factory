#!/usr/bin/env python3
"""Opt-in real Codex smoke of explicit exits (docs/explicit-exits-contract.md).

Needs a Core started with FACTORY_EXPLICIT_EXITS=1 and a node or worker with the Codex driver and
factory-mcp. Uses a project without a repository (no git facts needed). Consumes subscription usage.
"""
import json
import os
from pathlib import Path
import time
import urllib.request
import uuid

base = os.environ.get('FACTORY_URL', 'http://127.0.0.1:8080')
token = os.environ['FACTORY_TOKEN']
project = 'exits-smoke-' + uuid.uuid4().hex[:8]
workspace = Path('.cache/' + project).resolve()
workspace.mkdir(parents=True, exist_ok=True)


def api(method, path, body=None):
    req = urllib.request.Request(base + path, method=method,
        data=json.dumps(body).encode() if body is not None else None,
        headers={'Authorization': 'Bearer ' + token, 'Content-Type': 'application/json'})
    with urllib.request.urlopen(req, timeout=15) as response:
        return json.load(response)


def wait(task_id, statuses, seconds=330):
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        task = api('GET', '/tasks/' + task_id)
        if task['status'] in statuses:
            return task
        assert task['status'] not in ('FAILED', 'CANCELLED'), task
        time.sleep(1)
    raise TimeoutError('task ' + task_id + ' did not reach ' + str(statuses) + ': ' + json.dumps(api('GET', '/tasks/' + task_id)))


def run_of(task):
    run = api('GET', '/runs/' + task['current_run_id'])
    while run['status'] in ('QUEUED', 'RUNNING', 'CANCELLING'):
        time.sleep(1)
        run = api('GET', '/runs/' + task['current_run_id'])
    return run


api('POST', '/projects', {'id': project, 'name': project, 'workspace': str(workspace)})

submit_text = ('Use only Factory MCP tools, no shell and no files. Read the project with factory_project_get. '
    'Then end the run with factory_exit_submit, summary EXIT_SUBMIT_OK and checks ["read the project"]. '
    'Do nothing after the exit.')
submit = api('POST', '/tasks', {'project_id': project, 'title': 'Exit submit', 'description': submit_text,
    'assignee': 'demo', 'ready': True, 'request_id': project + '-submit'})
submit = wait(submit['id'], ('IN_REVIEW',))
submit_run = run_of(submit)
assert submit['submission']['summary'] == 'EXIT_SUBMIT_OK', submit
assert submit['implementer'] == 'demo', submit
assert submit_run['outcome'] == 'succeeded', submit_run
calls = api('GET', '/runs/' + submit['current_run_id'] + '/tool-calls')
assert any(c['tool'] == 'factory_exit_submit' and c['status'] == 200 for c in calls), calls
submit = api('GET', '/tasks/' + submit['id'])
api('POST', '/tasks/' + submit['id'] + '/review', {'version': submit['version'], 'accept': True, 'request_id': project + '-accept'})
assert api('GET', '/tasks/' + submit['id'])['status'] == 'DONE'

done_text = ('Use only Factory MCP tools, no shell and no files. This task needs no work: '
    'end the run at once with factory_exit_done, summary EXIT_DONE_OK.')
done = api('POST', '/tasks', {'project_id': project, 'title': 'Exit done', 'description': done_text,
    'assignee': 'demo', 'ready': True, 'request_id': project + '-done'})
done = wait(done['id'], ('DONE',))
done_run = run_of(done)
assert done['result'] == 'EXIT_DONE_OK', done
assert done_run['outcome'] == 'succeeded', done_run

print(json.dumps({'project': project, 'submit': {'task': submit['id'], 'run': submit_run['spec']['id'], 'run_status': submit_run['status'], 'outcome': submit_run['outcome']},
    'done': {'task': done['id'], 'run': done_run['spec']['id'], 'run_status': done_run['status'], 'outcome': done_run['outcome']},
    'tool_calls': [c['tool'] + ':' + str(c['status']) for c in calls]}, indent=2))
