# GitHub sync and GitHub Actions

A repository can be linked to a GitHub repository (Settings > GitHub) through
a GitHub connection (an App installation or a fine-grained token):

- **mirror**: the repository follows GitHub and refuses pushes;
- **backup**: branches and tags are pushed to GitHub after every push;
- **sync**: both directions, fast-forward only; a divergence keeps the GitHub
  commit on `sync-conflict/github/<branch>`.

A repository can also be imported once from GitHub (git data, labels,
milestones, issues, pull requests, releases and, with the **actions** kind,
the Actions configuration and run history).

## Workflows of a GitHub repository

Workflow files in `.github/workflows` come along with git. The Actions page
lists the workflow files of the default branch, plus every workflow that ran
here or (imported) on GitHub. A file in `.ervisio/workflows` hides the file
with the same name in `.github/workflows`.

## Run Actions on synced pushes

Commits written by the sync (mirror and sync modes) have no user behind them,
so by default they start no workflow. Turn on **Run Actions on synced
pushes** on the link (`runActions` in `PUT /repos/{owner}/{name}/github/sync`)
to run the workflows of the repository for them, like a push:

- the runs show `ervisio-sync[bot]` as actor (`github.actor`, `sender` and
  `pusher` of the event);
- schedules of the default branch are refreshed by these pushes too;
- pushes made by workflows (job tokens) never start new runs, and pushes of
  Ervisio to GitHub (backup, sync) create nothing locally, so nothing runs
  twice here. GitHub still runs its own copy of the workflows on GitHub.

## Bot actor

The writes of the sync (mirror, sync and import fetches) are made by the
system bot `ervisio-sync[bot]` (display name "Ervisio Sync"): the activity
feed, notifications and webhook `sender` show it with a "bot" badge, and
the REST API returns it with `"type": "Bot"`. Bots cannot sign in. Activity
recorded before version 1.11 has no actor and still shows as "ErvisioSync".
Imported issues, pull requests and comments whose GitHub author has no local
account keep their current behaviour (no local author, the GitHub login is
kept as text).

Workflow job tokens act as `ervisio-actions[bot]` ("Ervisio Actions"): the
comments, issues, releases, statuses, check runs and pushes they make are
attributed to it, and none of them starts a workflow.

## Import of variables, secrets and runs

Settings > Secrets (or Variables) shows **Import from GitHub** on a linked or
imported repository (`POST /repos/{owner}/{name}/actions/github/import`). It
reads, with the connection of the link (or of the import):

- the repository variables, the environments and their variables: existing
  variables are kept unless "Replace the variables that already exist" is set;
- the names of the secrets (repository and environments): GitHub never returns
  their values, so they are listed as **secrets to fill in** until a secret
  with that name exists here;
- the latest 100 workflow runs, kept read-only (at most 500 per repository):
  the run list shows them with a GitHub badge and links to their logs on
  GitHub. They are never scheduled or run here.

After the first import the run history is refreshed at every sync run of the
link. A connection without the needed read permission (Actions, Variables,
Secrets, Environments for an App) gets a clear message in the import state
instead of a failure.
