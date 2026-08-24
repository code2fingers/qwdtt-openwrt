# Working rules for this repo

## Before coding

Read the documentation first: this file, the README, and the other Markdown in
the repository. Much of what looks like a free choice has already been decided
there, and the reason is rarely visible from the code alone.

## Code

KISS, and small. The best version of a change is the one that adds the least:
fewer branches, fewer options, fewer layers, fewer lines to read before the
intent is clear.

Solve the case in front of you, not the ones someone might want later. An
option, an abstraction or a helper added "for when we need it" is paid for on
every read and usually never earns it back. Where extending and deleting are
equally close, delete.

## Comments

Code should read without commentary. Do not restate what the code already
says, and do not leave notes that the next refactor will outdate. Dense
comments are harder for a human to read past than the code itself.

Write a comment only where the code is genuinely surprising and would mislead
someone without it: a workaround for another project's bug, a constant that
was measured rather than chosen, a literal that looks translatable but is
load-bearing. Keep it to a line or two. If the reason needs a paragraph, it is
not a comment.

## Commits

Scope first, then a short subject: `client:`, `ci:`, `docs:`, `comments:`,
`files:`. One line, and stop there. Add a body only when the change has a
reason that is not visible in the diff -- a constraint that forced it, or an
exception that would otherwise look like an oversight.

## Merging

`main` takes pull requests only; direct pushes are refused for everyone,
admins included. An agent opens the PR, pushes to its branch, and stops there.
Merging is done by hand -- never by an agent -- so that a person has read the
diff before it lands. `.claude/settings.json` denies `gh pr merge` to stop it
happening by reflex, but the rule matters more than the guard: there is more
than one way to merge a pull request.

Release bumps are no exception. The `PKG_VERSION` commit goes through a PR
like anything else, and the tag is pushed only once that has been merged.

## Reference only what the reader can see

A code comment, a commit message, an issue or a pull request is read by
someone who has the repository and nothing else. Everything outside it is
invisible to them: files under ignored folders, a branch that was never
pushed, a scratch review document, and above all the short labels such
documents use. Those read as if they were common knowledge and are not.

Name the thing rather than its label. "the hardcoded target list in the
download table" still means something a year from now; a short label means
nothing to a reader who never saw that list, and nothing to you either once
the file is gone.

## Language

Code, comments, identifiers, log output are English.
No em dashes; use `-`.

Issues are Russian: their titles, their bodies, and every comment on them.
That is where users arrive, and the users of this project are mostly
Russian-speaking, so it is the one place worth meeting them in their own
language.

The installation docs are Russian for the same reason: `README.md` and
`docs/custom-feed.md` are read at a router with a browser open, by someone
who wants the thing working rather than changed.

Pull requests are English, titles and bodies alike, along with their review
comments. A pull request argues about the code it changes, in the same
terms as the code, and is read by whoever maintains the repository rather
than by whoever installs it.

The split is by audience, not by file: a Russian identifier in the code
reaches nobody at all, and a Russian issue reply reaches exactly the person
who asked.
