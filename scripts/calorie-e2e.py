#!/usr/bin/env python3
"""Start/observe the real calorie-calculator team through existing owner APIs.
Uses FACTORY_TOKEN from the environment; never prints credentials.
"""
import argparse
import json
import os
from pathlib import Path
import time
import urllib.error
import urllib.request

ROOT = Path(__file__).resolve().parent.parent
PROJECT = 'calorie-calculator'
STATE = ROOT / '.cache/calorie-e2e/state.json'
BASE = os.environ.get('FACTORY_URL', 'http://127.0.0.1:8080').rstrip('/')

def api(method, path, body=None):
    token = os.environ.get('FACTORY_TOKEN')
    if not token:
        raise SystemExit('Load .env first: set -a; source .env; set +a')
    req = urllib.request.Request(BASE + path, method=method,
        data=json.dumps(body).encode() if body is not None else None,
        headers={'Authorization': 'Bearer ' + token, 'Content-Type': 'application/json'})
    with urllib.request.urlopen(req, timeout=20) as response:
        return json.load(response)

def save(state):
    STATE.parent.mkdir(parents=True, exist_ok=True)
    STATE.write_text(json.dumps(state, indent=2, ensure_ascii=False) + '\n')

def start():
    state = json.loads(STATE.read_text()) if STATE.exists() else {'project': PROJECT}
    workspace = ROOT / 'projects' / PROJECT
    if not (workspace / 'PROJECT_BRIEF.md').is_file():
        raise SystemExit('Missing project brief; do not start an empty project')
    try:
        project = api('GET', '/projects/' + PROJECT)
    except urllib.error.HTTPError as error:
        if error.code != 404:
            raise
        project = api('POST', '/projects', {'id': PROJECT, 'name': 'Calorie Calculator',
            'workspace': str(workspace), 'description': 'Autonomous four-agent iOS calorie diary E2E; clean minimalist stylish interface; production release candidate; dedicated accessibility work deferred by owner.'})
    assert project['workspace'] == str(workspace), 'Existing project uses another workspace'
    state['workspace'] = str(workspace)
    if 'conversation_id' not in state:
        conv = api('POST', '/conversations', {'id': 'calorie-owner-dialog', 'project_id': PROJECT,
            'agent': 'calorie-manager', 'title': 'Calorie calculator — owner and autonomous manager', 'request_id': 'calorie-owner-dialog-v1'})
        state['conversation_id'] = conv['id']
        save(state)
    if 'kickoff_message_id' not in state:
        message = api('POST', '/conversations/' + state['conversation_id'] + '/messages', {
            'body': 'Start delivery of the calorie calculator. Read PROJECT_BRIEF.md in the workspace for the full owner brief and release gate. We want a clean, minimalist but stylish native iOS interface and a production-quality application, with real builds/tests/QA and release materials. IMPORTANT owner correction: dedicated accessibility work/audit/tests are deferred and are NOT a gate for this test. You have delegated product, design, technical and quality authority; make normal decisions yourself without asking me clarifying questions. Work with calorie-designer, calorie-ios and calorie-qa. Use OpenSpec, the decision registry and Factory tasks. Create and make-ready the first specialist task now, and answer this message with the initial plan. Continue the whole delivery through team review notifications, create follow-up tasks yourself, handle developer questions independently and have QA independently verify actual implementation. Do not ask the owner to manually advance stages. Finish each turn to release the single writer. Publication/uploads/purchases and production credentials are outside this local assignment; prepare everything locally and document exact external prerequisites honestly. Deliver docs/RELEASE_READINESS.md with actual evidence and READY/NOT_READY assessment. Do not fake test questions/defects or successful checks. Begin now.',
            'request_id': 'calorie-kickoff-v1'})
        state['kickoff_message_id'] = message['id']
        save(state)
    print(json.dumps(state, indent=2, ensure_ascii=False), flush=True)

def status(as_json=False):
    state = json.loads(STATE.read_text()) if STATE.exists() else {'project': PROJECT}
    tasks = api('GET', '/tasks?project=' + PROJECT)
    runs = [r for r in api('GET', '/runs') if r['spec']['project'] == PROJECT]
    inbox = api('GET', '/inbox?project=' + PROJECT)
    messages = api('GET', '/conversations/' + state['conversation_id'] + '/messages') if state.get('conversation_id') else []
    value = {'project': PROJECT, 'tasks': tasks, 'runs': runs, 'owner_inbox': inbox, 'owner_dialog': messages}
    if as_json:
        print(json.dumps(value, indent=2, ensure_ascii=False), flush=True)
        return
    print(time.strftime('%Y-%m-%d %H:%M:%S'), PROJECT, flush=True)
    for t in tasks:
        print(t['status'].ljust(12), t['assignee'].ljust(18), t['title'], '[' + t['id'] + ']', flush=True)
    for r in runs:
        if r['status'] in ('QUEUED', 'RUNNING', 'CANCELLING', 'UNKNOWN', 'FAILED'):
            print('RUN', r['status'], r['spec']['agent'], r['spec']['id'], flush=True)
    print('Runs:', len(runs), '| Owner inbox:', len(inbox), flush=True)
    answers = [m for m in messages if m['sender'] == 'calorie-manager']
    if answers:
        print('Latest manager answer:', answers[-1]['body'], flush=True)

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('command', choices=['start', 'status', 'watch', 'send'])
    parser.add_argument('text', nargs='?')
    parser.add_argument('--json', action='store_true')
    args = parser.parse_args()
    if args.command == 'start':
        start()
    elif args.command == 'send':
        if not args.text:
            parser.error('send requires message text')
        state = json.loads(STATE.read_text())
        import uuid
        message = api('POST', '/conversations/' + state['conversation_id'] + '/messages',
            {'body': args.text, 'request_id': 'owner-' + uuid.uuid4().hex})
        print(json.dumps({'message_id': message['id'], 'conversation_id': state['conversation_id']}))
    elif args.command == 'watch':
        try:
            while True:
                status()
                time.sleep(5)
        except KeyboardInterrupt:
            return
    else:
        status(args.json)

if __name__ == '__main__':
    main()
