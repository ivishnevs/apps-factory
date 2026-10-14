#!/usr/bin/env python3
"""Opt-in real Codex smoke of the agent merge flow (task-worktrees 3.3):
implement -> factory_exit_submit (adapter pushes the task branch and opens the PR) -> owner approves ->
the implementer merges the PR -> factory_exit_done (accepted only with the PR merged).

Needs: Core with FACTORY_EXPLICIT_EXITS=1 whose agent SMOKE_AGENT (default demo) has workspace.mode worktree,
a node with worktrees enabled and gh logged in, and an empty GitHub repository REPO_URL (ssh) / REPO (owner/name)
with a main branch. Consumes subscription usage.
"""
import json
import os
from pathlib import Path
import subprocess
import time
import urllib.request
import uuid

base = os.environ.get('FACTORY_URL', 'http://127.0.0.1:8080')
token = os.environ['FACTORY_TOKEN']
agent = os.environ.get('SMOKE_AGENT', 'demo')
repo, repo_url = os.environ['REPO'], os.environ['REPO_URL']
project = 'merge-smoke-' + uuid.uuid4().hex[:8]
workspace = Path('.cache/' + project).resolve()
workspace.mkdir(parents=True, exist_ok=True)


def api(method, path, body=None):
    req = urllib.request.Request(base + path, method=method,
        data=json.dumps(body).encode() if body is not None else None,
        headers={'Authorization': 'Bearer ' + token, 'Content-Type': 'application/json'})
    with urllib.request.urlopen(req, timeout=15) as response:
        return json.load(response)


def wait(task_id, statuses, seconds=900):
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        task = api('GET', '/tasks/' + task_id)
        if task['status'] in statuses:
            return task
        assert task['status'] not in ('FAILED', 'CANCELLED'), task
        time.sleep(2)
    raise TimeoutError(json.dumps(api('GET', '/tasks/' + task_id)))


def prs(state):
    out = subprocess.run(['gh', 'pr', 'list', '--repo', repo, '--state', state, '--json', 'number,state,headRefName'],
        check=True, capture_output=True, text=True).stdout
    return json.loads(out)


api('POST', '/projects', {'id': project, 'name': project, 'workspace': str(workspace), 'repo_url': repo_url, 'default_branch': 'main'})
text = ('Add a file MERGE_SMOKE.md containing exactly the line "merge smoke ok", commit it on your task branch '
    '(git add + git commit; the working tree must be clean), then end the run with factory_exit_submit, '
    'summary MERGE_SUBMIT_OK, checks ["file committed"]. When your task comes back approved, merge its pull '
    'request with gh pr merge --squash, then end with factory_exit_done, summary MERGE_DONE_OK.')
task = api('POST', '/tasks', {'project_id': project, 'title': 'Merge smoke', 'description': text,
    'assignee': agent, 'ready': True, 'request_id': project + '-task'})
task = wait(task['id'], ('IN_REVIEW',))
assert task['submission']['summary'] == 'MERGE_SUBMIT_OK', task
open_prs = prs('open')
assert len(open_prs) == 1, open_prs
api('POST', '/tasks/' + task['id'] + '/review', {'version': task['version'], 'accept': True, 'request_id': project + '-approve'})
back = api('GET', '/tasks/' + task['id'])
assert back['status'] == 'READY' and back['assignee'] == agent, back
task = wait(task['id'], ('DONE',))
merged = prs('merged')
assert len(merged) == 1 and merged[0]['number'] == open_prs[0]['number'], merged
runs = [r for r in api('GET', '/runs') if r['spec']['project'] == project]
print(json.dumps({'project': project, 'task': task['id'], 'pr': merged[0], 'result': task['result'],
    'runs': [(r['spec']['agent'], r['status'], r.get('outcome')) for r in runs]}, indent=2))
