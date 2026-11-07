---
name: game-playtest
description: "Playtest a game build: scripted and headless runs, a 'what to try' checklist, defects with steps, expected and actual result and evidence."
license: Apache-2.0
metadata:
  categories: games, quality
  eval: "Playtest a new dash ability in a 3v3 match. Good: steps to reproduce each defect, frame time numbers, a what-to-try checklist."
---

1. Build and start the game the way the README says, headless where possible (bots, scripted clients, --quit-after, screenshots).
2. Play the feature of the task end to end, then its edges: first launch, join and leave, lag, many players, low frame rate, phone and desktop layouts.
3. Every defect: title, steps, expected, actual, frequency, build or commit, evidence (log excerpt, screenshot, video). One defect per report.
4. Measure what can be measured: frame time, memory, tick rate, load time; compare with the previous build.
5. For each playable milestone write a short 'what to try' list for people testing by hand on their machines.
