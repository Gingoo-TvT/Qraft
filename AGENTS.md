# Working on Qraft

Qraft is a standalone question-authoring workspace with a Windows client and a self-hosted service. This repository is the active development source.

- Keep the implementation simple and preserve complete user flows: fresh setup, connection, generation, verification, sets, assembly, and export.
- Never include credentials, instance configuration, user databases, question-bank dumps, private templates, or generated release binaries in Git.
- A fresh client must not contact a service before the user selects and saves one. A local instance must never adopt another instance's containers, volumes, networks, or credentials.
- Keep Web and desktop business pages shared. Native-only behavior belongs in `desktop/` and `frontend/src/desktop/`.
- Preserve third-party licenses. Production data must not become test fixtures; use synthetic fixtures.
- For Go logic changes, run the affected module's tests and build. For frontend changes, run lint, Web build and desktop build. Test installation/first launch and local runtime when changing those paths.
- Preserve unrelated uncommitted work. Stage explicit files, keep build output outside the checkout, and never publish or deploy without authorization.
