# ErvisioCode personal Remote relay

The personal relay supports locally approved PC sessions and creation in preapproved projects. Project selection is limited to the projects each linked host advertises; the repository catalog is a separate read-only feature.

Use one ErvisioRepo process behind the configured public HTTPS origin. Bind the upstream HTTP listener privately; TLS terminates at the HTTPS proxy. This relay intentionally does not share live leases or in-flight calls between processes. A restart loses the leases and calls; the host republishes locally approved sessions on its next poll.

The host is responsible for rechecking project and session approval immediately before dispatch. The central API authenticates the immutable account and linked device, and permits only `state`, `prompt`, and `interrupt` on a published session or `create` within an advertised project. Project advertisements contain opaque IDs and display labels, with optional Git provider, `owner/name`, and checkout mode. Advertisements are not persisted and never accept local paths, clone URLs, or credentials. A revoked host is no longer listed and cannot poll or receive new actions. Existing host-local tool permissions must still apply: a prompt is not a permission approval.

The API contract is in [`api/openapi.yaml`](../api/openapi.yaml) under `/user/ervisiocode/{sessions,projects,relay}`. Poll serially, but keep publishing while a separate clone runs. Hosts that advertise projects send `enabled: true` even when no sessions exist; use `enabled: false` to withdraw, then a new UUID before republishing. Older hosts can still withdraw with an empty session list. A competing lifetime receives `409` until the active one is withdrawn or expires. A separate checkout has up to 45 seconds; other requests still have up to 10 seconds. Poll requests are delivered at most once; after an HTTP timeout or lost reply, refresh the live session list rather than retrying a mutation automatically.

The database keeps each linked device's last enabled publication snapshot: session identifier, title, and last activity. `GET /sessions` returns metadata for entries that are offline under `recent` without a relay ID. Graceful withdrawal or expiry leaves the snapshot intact; an enabled snapshot removes previously published sessions that it omits, including when it is empty. Device revocation removes the snapshot too. Prompt and transcript transport is in memory only, with no server-side history.

An embedded `/code` client supplies its linked `accountId` and the scoped HTTPS hub cookie; the server does not fall back to the browser account. Embedded POSTs require JSON and the exact configured HTTPS origin. Browser sign-ins without an account selector continue to use the normal CSRF flow.

`GET /user/ervisiocode/catalog` lists the selected account's ErvisioRepo repositories and repositories available through its own GitHub.com connections. Organization and other accounts' connections are not used. The response includes live checkout approvals only when the provider and repository name match. Discovery never transfers provider credentials to the browser or host, and does not approve a project for checkout.

Before delivering a repository-backed create, the relay checks the exact live host approval and verifies current access to that repository independently of catalog pagination. Local-only projects do not require a provider check. For ErvisioRepo, public or internal visibility alone does not count as an account grant. For GitHub, a public repository is not sufficient proof by itself: a PAT must pass GitHub's collaborator check, which can reject read-only collaborators. An App connection must be able to obtain a fresh contents-read token scoped to that repository. Unverifiable access fails closed.

Host approvals currently identify a repository by provider and name, not an immutable provider ID. They reject changed advertisements and do not follow renamed repositories, but cannot detect deletion and recreation under the same name. Closing that gap requires storing the immutable repository ID when the host approves it and checking that ID at dispatch and checkout; until then, withdraw and reapprove affected projects after renames, transfers, or recreation.

## Sharing

Reader grants authorize existing live sessions only. A personal invite must be accepted by its named recipient. Organization access follows current membership. Restricted reads require a capable PC, and it applies the stored history boundary before sending a text-only result. The server checks the grant again before returning the result. It does not persist transcripts.

Proposals are disabled and historical proposer grants remain revoked.

### Text collaboration

On a compatible linked PC, an owner can invite people or share with an organization as **Collaborator** without enabling an experimental flag. The owner explicitly grants full access to the chat history; organization grants also require consent for future members. Replacing or removing an organization member does not transfer their individual permissions.

Collaborators write in the same chat as its owner. Both see the same timeline, including who sent each prompt. The owner and the contributor who sent a request can stop its generation. Collaborator prompts join the owner's session queue, and the PC's agent works on them with the owner's model, tools, files and permission settings; tool approvals appear only on the PC. The owner opts the PC in with `experimental.remote_collaboration` in its global ErvisioCode config. A Collaborator can stop only the run their own prompt started; the owner can stop any run. Restricted Reader views show collaborator prompts with their author. The chat also shows the agent's current todo list, taken from its latest update among the visible messages. Only the linked PC retains the transcript; ErvisioRepo relays it without storing it. The PC must be online for new messages. Revocation, removal from the organization, or loss of the host lease stop new prompts; a prompt that is already running or queued stays in the owner's chat.
