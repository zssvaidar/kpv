- Tasks are GitHub issues in https://github.com/zssvaidar/kpv/issues, one at a time
- An issue is groomed into _docs/task-template.md before anyone implements it
- Commit regularly, and only with the whole suite passing
- Every agent starts by reading `@CLAUDE.md`, then the `CLAUDE.md` of each
  area its issue touches. Claude Code does not load `@CLAUDE.md` by itself,
  because of the `@` in its name

Roles
- PM - grooms a task before anyone implements it, follows _docs/team/pm.md
- Engineer - implements one groomed task, follows _docs/team/software-engineer.md
- QA - checks the result against the acceptance criteria, follows _docs/team/qa-engineer.md
- Accelerator - shortens what agents read to find what they need, follows _docs/team/accelerator-01.md

Orchestrator

The main session is the orchestrator. It launches the PM, the engineer,
QA and the accelerator as subagents. It does not groom, implement or test itself.

Lifecycle

1. Pick the next open issue from the backlog whose `## Depends on` issues are
   closed
2. PM grooms it
3. Engineer implements it
4. QA verifies it
5. On FAIL, back to step 3 with the QA comment as input
6. On PASS, the orchestrator closes the issue
7. Repeat until the backlog is empty

Rules
- You can work on 2 issues at the time
- Do not skip step 2
- Only the orchestrator closes an issue, and only after QA outputs PASS
- After every 6th closed issue, the accelerator runs once before the next
  issue is picked
