# Security policy

## Supported versions

gitperch is pre-1.0. Security fixes are made on `main` and included in the
next release; older versions are not patched.

## Reporting a vulnerability

Please **do not** open a public issue for security problems.

Report vulnerabilities privately through GitHub's
[private vulnerability reporting](https://github.com/mark-lvl/gitperch/security/advisories/new).
Include a description, the affected version or commit, reproduction steps and
the impact you expect. You should receive an acknowledgement within a week.
We will keep you informed while a fix is prepared and credit you in the
advisory unless you prefer otherwise.

## Scope and threat model

gitperch runs Git with your normal user permissions. Please keep these
documented limits in mind when reporting:

- Git hooks, credential helpers and SSH configuration from the repositories
  and your Git config still run as Git normally runs them. gitperch is **not**
  a sandbox for untrusted repositories.
- External processes, including AI agents, can change a repository between
  gitperch's final revalidation and the Git command it runs.

In scope, for example: gitperch running an unreviewed Git mutation, a mutation
on a different repository, branch, remote or commit than the one shown;
command or argument injection; credential leaks in output; terminal escape
injection through repository data; or symlink tricks that escape discovery
rules.
