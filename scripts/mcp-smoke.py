#!/usr/bin/env python3
"""Opt-in real Codex MCP planner -> executor -> reviewer; start native Worker first."""
import json
import os
from pathlib import Path
import time
import urllib.request
import uuid

base = os.environ.get('FACTORY_URL', 'http://127.0.0.1:8080')
token = os.environ['FACTORY_TOKEN']
project = 'mcp-smoke-' + uuid.uuid4().hex[:10]
workspace = Path('.cache/' + project).resolve()
workspace.mkdir(parents=True, exist_ok=True)

def api(method, path, body=None):
    req = urllib.request.Request(base + path, method=method,
        data=json.dumps(body).encode() if body is not None else None,
        headers={'Authorization': 'Bearer ' + token, 'Content-Type': 'application/json'})
    with urllib.request.urlopen(req, timeout=15) as response:
        return json.load(response)

def wait(task_id, status):
    deadline = time.monotonic() + 330
    while time.monotonic() < deadline:
        task = api('GET', '/tasks/' + task_id)
        if task['status'] == status:
            return task
        assert task['status'] not in ('FAILED', 'CANCELLED', 'BLOCKED'), task
        time.sleep(.5)
    raise TimeoutError('Task did not reach ' + status + '; inspect before retrying')

api('POST', '/projects', {'id': project, 'name': project, 'workspace': str(workspace)})
worker_description = '''Use only Factory MCP, no shell and no file modifications. Read the current project with factory_project_get. Read openspec resource references/conventions.md with factory_skills_read and verify AGENT_CONFIG_OK. Read your current task with factory_task_get to obtain its version. Save a structured submission with factory_task_submit for your current task, summary MCP_EXECUTOR_OK, checks ["skill marker AGENT_CONFIG_OK verified"], request_id executor-submit. Finish with exactly MCP_EXECUTOR_OK.'''
planner_description = f'''Use only Factory MCP, no shell or files. Read the project. Create exactly two tasks in this project:
1. Title MCP executor, assignee demo, priority 10, description {json.dumps(worker_description)}, request_id create-executor. Make it ready using its returned version and request_id ready-executor.
2. Title MCP review, assignee manager, priority -10, request_id create-review. Its description must instruct the reviewer to use only MCP, read executor task ID (insert actual returned ID), verify its IN_REVIEW status, submission summary MCP_EXECUTOR_OK and check AGENT_CONFIG_OK, then accept it with factory_task_accept using current version and request_id accept-executor, then finish MCP_REVIEW_OK. No dependencies. Make review task ready with its returned version and request_id ready-review.
Do not wait for these tasks; this turn holds the writer. Finish MCP_PLANNER_OK.'''
planner = api('POST', '/tasks', {'project_id': project, 'title': 'MCP planner',
    'description': planner_description, 'assignee': 'manager', 'request_id': 'planner'})
api('POST', '/tasks/' + planner['id'] + '/actions', {'version': planner['version'], 'action': 'ready'})
planner = wait(planner['id'], 'IN_REVIEW')
all_tasks = api('GET', '/tasks?project=' + project)
assert len(all_tasks) == 3, all_tasks
executor = next(t for t in all_tasks if t['title'] == 'MCP executor')
review = next(t for t in all_tasks if t['title'] == 'MCP review')
assert executor['created_by'] == review['created_by'] == 'manager', all_tasks
review = wait(review['id'], 'IN_REVIEW')
executor = wait(executor['id'], 'DONE')
assert executor['submission']['summary'] == 'MCP_EXECUTOR_OK', executor
assert any('AGENT_CONFIG_OK' in check for check in executor['submission']['checks']), executor
assert 'MCP_REVIEW_OK' in review['result'], review
calls = {t['id']: api('GET', '/runs/' + t['current_run_id'] + '/tool-calls') for t in (planner, executor, review)}
assert any(c['tool'] == 'factory_task_create' and c['status'] == 200 for c in calls[planner['id']]), calls
assert any(c['tool'] == 'factory_task_submit' and c['status'] == 200 for c in calls[executor['id']]), calls
assert any(c['tool'] == 'factory_task_accept' and c['status'] == 200 for c in calls[review['id']]), calls
for task in (planner, review):
    api('POST', '/tasks/' + task['id'] + '/review', {'version': task['version'], 'accept': True})
summary = {'project': project, 'tasks': [{'id': t['id'], 'agent': t['assignee'], 'status': 'DONE',
    'run_id': t['current_run_id'], 'tool_calls': len(calls[t['id']])} for t in (planner, executor, review)], 'calls': calls}
Path('.cache/mcp-verification.json').write_text(json.dumps(summary, indent=2))
print(json.dumps(summary, indent=2))
