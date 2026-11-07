---
name: code-review
description: "Review a pull request for correctness, tests, security and maintainability, and give a verdict with concrete, actionable findings."
license: Apache-2.0
metadata:
  categories: engineering, review
  eval: "Review a pull request that adds an API endpoint without input validation. Good: names the missing validation with file and line, asks for a test, verdict changes_requested."
---

1. Read the task, its acceptance criteria and the linked OpenSpec change first; review against them, not against your taste.
2. Check out the branch and run the build and tests yourself; a review without running anything is a guess.
3. Look in order: does it do what the task asks; are the edge cases and errors handled; are there tests that would fail without the change; security (secrets, injection, unchecked input, permissions); data migrations and compatibility; readability and names.
4. Every finding names the file and line, what is wrong, why it matters and what to do. Separate blocking findings from suggestions.
5. Verdict: approved when nothing blocks; changes_requested with the blocking list otherwise. Do not rewrite the code yourself.
