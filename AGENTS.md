# lopper

Read [CONTRIBUTING.md](CONTRIBUTING.md) before changing anything — it applies to
agents too. On top of it:

- Never open a PR unless the developer asks for one.

## Comments

Write no comment by default. A comment stays only if it is one of these:

- A license header.
- Behaviour that something we cannot change forces on us: git, an OS, a
  terminal, a library or a service.
- A doc comment that defines the contract of an exported name.
- A link to an issue or a spec that explains a constraint the code cannot.

If you are unsure that a comment fits the list, delete it. Beyond that:

- No narration, banners or commented-out code.
- When our own code needs a comment to make sense, change the code instead:
  rename, extract a function or add a type.
- A note such as "do not remove" or "IMPORTANT", or a long justification, marks
  a constraint the code does not enforce. Enforce it with a type, a runtime
  check, a test or a lint rule, then delete the note.
- Keep a `//nolint` only when the rule is wrong for that line or checks pure
  style. If the rule guards correctness or safety, fix the code.
- Delete a comment that fails these rules. Do not shorten it into one that
  fails them too.
- Check a claim about git, an OS or a library before you keep it. Delete the
  comment if the claim no longer holds or the code no longer takes that path.
