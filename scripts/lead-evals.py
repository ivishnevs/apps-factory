#!/usr/bin/env python3
"""Lead behaviour evals (lead-brainstorm-mode 2.3, 2.5): talk to a team lead in a project's chat and check what it did.

Each scenario sends one message as the owner, waits for the lead's reply and judges it from Core's records:
  clear-request  "Create a task: ..." with a full description  -> a task is created, the reply asks nothing
  unclear-idea   a loose idea                                   -> no task yet; ONE reply with the questions together,
                                                                    each with a recommended answer
  in-direction   a choice the project's direction answers      -> no question; the lead says what it decided
  follow-up      a small follow-up to the previous request     -> handled without questions

Usage: FACTORY_URL=... FACTORY_TOKEN=... scripts/lead-evals.py --company CO --project P --lead AGENT [--only NAME]
       [--dry-run]. It costs real runs of the lead: use an evaluation project, not a live one.
"""
import argparse, json, os, re, sys, time, urllib.request

SCENARIOS = [
    ("clear-request", "Create a task: add a 'Copy link' button to the share dialog. Acceptance: it copies the page URL and shows 'Copied' for 2 seconds. Assign it to whoever builds the UI.",
     dict(min_tasks=1, max_questions=0)),
    ("unclear-idea", "I've been thinking we could do something about retention, maybe some kind of rewards?",
     dict(max_tasks=0, batched_questions=True)),
    ("in-direction", "Should the empty state of the share dialog use the same illustration style as the rest of the app?",
     dict(max_tasks=0, max_questions=0, decides=True)),
    ("follow-up", "Also make the 'Copied' text say 'Link copied'.", dict(max_questions=0)),
]

def call(method, path, body=None):
    req = urllib.request.Request(os.environ["FACTORY_URL"] + path, method=method, data=json.dumps(body).encode() if body is not None else None,
                                 headers={"Authorization": "Bearer " + os.environ["FACTORY_TOKEN"], "X-Factory-Company": ARGS.company, "Content-Type": "application/json"})
    with urllib.request.urlopen(req) as r:
        return json.load(r)

def questions_in(text):
    return len(re.findall(r"\?\s*$", text, re.M))

def judge(name, want, reply, new_tasks):
    problems = []
    q = questions_in(reply["body"]) + len(reply.get("questions") or [])
    if "min_tasks" in want and len(new_tasks) < want["min_tasks"]:
        problems.append(f"expected a task, got {len(new_tasks)}")
    if "max_tasks" in want and len(new_tasks) > want["max_tasks"]:
        problems.append(f"created {len(new_tasks)} tasks before agreeing a plan")
    if "max_questions" in want and q > want["max_questions"]:
        problems.append(f"asked {q} questions where none were needed")
    if want.get("batched_questions"):
        if q == 0:
            problems.append("asked nothing about an unclear idea")
        if not re.search(r"recommend|suggest|I'd go with|my pick|default", reply["body"], re.I):
            problems.append("questions without recommended answers")
    if want.get("decides") and not re.search(r"decid|I'll|I will|going with|we'll use", reply["body"], re.I):
        problems.append("did not say what it decided")
    return problems

def main():
    conv = f"owner-{ARGS.lead}-{ARGS.project}"
    failed = 0
    for name, text, want in SCENARIOS:
        if ARGS.only and name != ARGS.only:
            continue
        if ARGS.dry_run:
            print(f"{name}: would send {text!r}; expects {want}")
            continue
        before = {t["id"] for t in call("GET", "/tasks?project=" + ARGS.project)}
        sent = call("POST", "/messages", {"project_id": ARGS.project, "body": f"@{ARGS.lead} {text}", "request_id": f"eval-{name}-{int(time.time())}"})
        reply, deadline = None, time.time() + ARGS.timeout
        while time.time() < deadline and reply is None:
            time.sleep(15)
            for m in call("GET", "/messages?conversation=" + conv) or []:
                if m["sender"] == ARGS.lead and m["created_at"] >= sent["created_at"] and m["id"] != sent["id"]:
                    reply = m
        if reply is None:
            print(f"FAIL {name}: no reply in {ARGS.timeout}s"); failed += 1; continue
        time.sleep(30)  # tasks the reply announces
        new = [t for t in call("GET", "/tasks?project=" + ARGS.project) if t["id"] not in before and t.get("kind") != "conversation"]
        problems = judge(name, want, reply, new)
        print(("PASS " if not problems else "FAIL ") + name + ("" if not problems else ": " + "; ".join(problems)))
        failed += bool(problems)
    sys.exit(1 if failed else 0)

p = argparse.ArgumentParser()
p.add_argument("--company", required=True); p.add_argument("--project", required=True); p.add_argument("--lead", required=True)
p.add_argument("--only"); p.add_argument("--timeout", type=int, default=900); p.add_argument("--dry-run", action="store_true")
ARGS = p.parse_args()
if __name__ == "__main__":
    main()
