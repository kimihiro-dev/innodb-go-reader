# Repository Instructions

## Current State Is the Source of Truth

Read these files at the start of each session:

| File | Purpose |
|---|---|
| `docs/REQUIREMENTS.md` | Current scope, acceptance criteria and non-goals |
| `docs/CONVENTIONS.md` | Engineering, testing and documentation rules |
| `docs/TODO.md` | Active and explicitly approved pending tasks |
| `docs/format/83-release-support-matrix.md` | Detailed supported inputs and API contracts |

Use the current implementation and these documents to understand the project.
Follow the latest commit on the current branch (HEAD) for development and builds;
do not pin the project to a tag or a designated commit. Existing tags identify
historical releases only. Record the actual source commit for build artifacts.
Do not recreate chronological decision logs, completed-task ledgers, historical
stage plans or archives. The user has chosen to remove those records.
Technical manuals, fixed fixtures and independent validation evidence remain.

If a new request conflicts with the current documented scope, identify the
conflict. Follow an explicit user override; otherwise ask which scope applies.
Do not infer supported combinations from old tests or similar implementations.

## Before Implementing a Feature

1. State the problem in one sentence and identify measurable acceptance criteria.
2. Search the standard library and existing dependencies for a reusable solution.
3. Consider the simplest solution, record the selected current contract in
   `docs/REQUIREMENTS.md`, and resolve open questions before implementing.
4. Put active work in `docs/TODO.md`; remove finished entries when the current
   documentation and relevant checks are complete.

## Engineering and Verification

- Make the minimum requested change; avoid unrelated refactoring or formatting.
- Match existing structure and style; remove only code made unused by your change.
- Keep unsupported, corrupt, incomplete and successful results distinguishable.
- Test meaningful behavior and failure boundaries appropriate to the change.
- Keep fixtures and raw evidence unchanged; corrupt only disposable copies.
- Update usage, technical scope and examples with each behavior change.
- Run `python3 scripts/check_docs.py` for Markdown changes and inspect the diff.
- Follow `docs/CONVENTIONS.md` for remaining implementation and document details.
