#!/usr/bin/env python3
"""Opt-in real Codex: artifact v1 approval, v2 rejection, v3 revision and gated QA."""
import hashlib
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
project = 'artifacts-smoke-' + uuid.uuid4().hex[:10]
workspace = Path('.cache/' + project).resolve()
workspace.mkdir(parents=True, exist_ok=True)

def api(method, path, body=None):
    req = urllib.request.Request(base + path, method=method,
        data=json.dumps(body).encode() if body is not None else None,
        headers={'Authorization': 'Bearer ' + token, 'Content-Type': 'application/json'})
    with urllib.request.urlopen(req, timeout=15) as response:
        return json.load(response)

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

def ready(task):
    return api('POST', '/tasks/' + task['id'] + '/actions', {'version': task['version'], 'action': 'ready'})

def accept(task):
    return api('POST', '/tasks/' + task['id'] + '/review', {'version': task['version'], 'accept': True})

api('POST', '/projects', {'id': project, 'name': project, 'workspace': str(workspace)})
results = []
consumer = None
for version, outcome in (('v1', 'approve'), ('v2', 'reject'), ('v3', 'approve')):
    asset = workspace / ('mockup-' + version + '.svg')
    asset.write_text('<svg xmlns="http://www.w3.org/2000/svg" width="320" height="160"><rect width="320" height="160" fill="#eef2ff"/><text x="20" y="80">Mockup ' + version + '</text></svg>\n')
    digest = hashlib.sha256(asset.read_bytes()).hexdigest()
    ref = {'id': 'mockup', 'version': version, 'uri': 'sha256:' + digest}
    qa_prompt = 'Use the exact owner-approved artifact inputs pinned in your run prompt. Use factory_artifact_get to verify mockup VERSION is APPROVED and matches the pinned URI. Read FILE and verify its SHA256 matches DIGEST. Never modify files or request new approval. Finish ARTIFACT_QA_VERSION_OK.'.replace('VERSION', version).replace('FILE', str(asset)).replace('DIGEST', digest)
    if version != 'v3':
        consumer = api('POST', '/tasks', {'project_id': project, 'title': 'QA ' + version,
            'description': qa_prompt, 'assignee': 'manager', 'required_artifacts': [ref], 'request_id': 'qa-' + version})
        consumer = ready(consumer)
    else:
        consumer = api('GET', '/tasks/' + consumer['id'])
        consumer = api('PATCH', '/tasks/' + consumer['id'], {'version': consumer['version'],
            'title': 'QA revised v3', 'description': qa_prompt, 'required_artifacts': [ref], 'request_id': 'revise-qa'})
    assert consumer['status'] == 'BLOCKED' and consumer['attempt'] == 0, consumer
    prompt = '''Act as the design-producing agent in this workflow test. Do not modify files or delegate. The SVG fixture FILE was already prepared. Register exactly ARTIFACT_JSON via factory_artifact_register, request_id register-VERSION. Read your current task via factory_task_get and save a structured submission with summary "Prepared mockup VERSION" and artifacts containing that exact reference, request_id submit-VERSION. Then call factory_artifact_request_approval with artifact_id mockup, artifact_version VERSION, reason "Review exact mockup VERSION", blocking false, request_id ask-VERSION. Finish DESIGN_VERSION_READY. Do not approve anything or poll.'''.replace('FILE', str(asset)).replace('ARTIFACT_JSON', json.dumps(ref)).replace('VERSION', version)
    producer = api('POST', '/tasks', {'project_id': project, 'title': 'Design ' + version,
        'description': prompt, 'assignee': 'demo', 'request_id': 'design-' + version})
    ready(producer)
    producer = wait('/tasks/' + producer['id'], lambda t: t['status'] == 'IN_REVIEW')
    assert ('DESIGN_' + version + '_READY') in producer['result'], producer
    producer = accept(producer)
    view = api('GET', '/projects/' + project + '/artifacts/mockup/' + version)
    grant = view['approval']
    assert grant['kind'] == 'artifact' and grant['artifact'] == ref and grant['status'] == 'PENDING', view
    still_waiting = api('GET', '/tasks/' + consumer['id'])
    assert still_waiting['status'] == 'BLOCKED' and still_waiting['attempt'] == 0, still_waiting
    inbox = api('GET', '/inbox?project=' + project)
    assert any(m.get('approval_id') == grant['id'] for m in inbox), inbox
    if version == 'v1' and '--restart-core' in sys.argv:
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
        assert api('GET', '/approvals/' + grant['id']) == grant
    decision = {'version': grant['version'], 'action': outcome, 'reason': 'Owner artifact smoke ' + outcome, 'request_id': 'owner-' + version}
    resolved = api('POST', '/approvals/' + grant['id'] + '/decision', decision)
    assert api('POST', '/approvals/' + grant['id'] + '/decision', decision) == resolved
    if outcome == 'approve':
        consumer = wait('/tasks/' + consumer['id'], lambda t: t['status'] == 'IN_REVIEW')
        assert 'ARTIFACT_QA_' + version + '_OK' in consumer['result'], consumer
        attempts = api('GET', '/tasks/' + consumer['id'] + '/attempts')
        inputs = attempts[-1]['spec']['approved_artifacts']
        assert inputs == [{'artifact': ref, 'approval_id': grant['id'], 'approval_version': resolved['version']}], inputs
        consumer = accept(consumer)
    else:
        consumer = api('GET', '/tasks/' + consumer['id'])
        assert consumer['status'] == 'BLOCKED' and consumer['attempt'] == 0, consumer
    results.append({'version': version, 'outcome': outcome, 'artifact': ref, 'approval': resolved,
        'producer_task': producer['id'], 'consumer_task': consumer['id'], 'consumer_status': consumer['status']})
# Nonblocking approval answers are independent message runs; let them finish before stopping Worker.
wait('/runs', lambda rs: not any(r['spec']['project'] == project and r['status'] in ('QUEUED', 'RUNNING', 'CANCELLING', 'UNKNOWN') for r in rs))
assert not api('GET', '/inbox?project=' + project)
summary = {'project': project, 'results': results, 'inbox_empty': True}
Path('.cache/artifact-approvals-verification.json').write_text(json.dumps(summary, indent=2) + '\n')
print(json.dumps(summary, indent=2))
