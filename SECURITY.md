# Security Policy

Twilight Core is a Cosmos SDK / CometBFT Proof-of-Authority blockchain. Consensus,
validator admission through `x/coreslot`, validator-set updates, and reward/economic
accounting are security-critical.

A vulnerability may halt the chain, fork it, corrupt state, manipulate the validator set,
or incorrectly issue, account for, or pay `utwlt`. We take security disclosures seriously
and appreciate responsible reporting.

## Reporting a vulnerability

**Do not open a public issue, pull request, or discussion for a security vulnerability.**

Report vulnerabilities privately through **GitHub Private Vulnerability Reporting**: on this
repository, go to **Security → Report a vulnerability**. This opens a private advisory
visible only to maintainers. (A dedicated security contact address and PGP key may be added
as the project grows.)

Please include as much of the following as possible:

- A description of the issue and its impact.
- The affected module, component, commit, branch, or release.
- Reproduction steps or a proof of concept.
- A failing test, localnet scenario, or drill, if available.
- Any suggested remediation.
- Whether the issue may affect consensus safety, liveness, validator admission, token
  accounting, private data, or operator security.

## What to expect

- Acknowledgement within a few business days.
- Initial assessment and severity triage.
- Follow-up questions where needed to reproduce or validate the report.
- Regular updates while a fix is prepared.
- Coordinated disclosure once a fix is released and operators have had a reasonable window
  to upgrade.

We will credit the reporter in the advisory unless they prefer to remain anonymous.

## Scope

A security issue in this repository is in scope if it can affect:

- consensus safety or liveness;
- deterministic state execution;
- validator admission, removal, rotation, or active-set updates;
- `ValidatorUpdate` provenance;
- reward, emission, or token accounting;
- genesis import/export correctness;
- transaction validation or authorization;
- CLI, REST, or gRPC behavior that can cause unsafe state changes or unsafe operator
  behavior;
- exposure of secrets, private keys, validator keys, mnemonics, RPC credentials, or
  sensitive operational data.

Examples of in-scope areas: `x/coreslot`; `x/rewards` (reward, emission, and economic
accounting); `app/` wiring; keeper logic; message handlers; BeginBlock and EndBlock
handlers; genesis handling; validator-set update paths; and security-sensitive CLI, REST,
and gRPC surfaces.

## Out of scope

The following are generally out of scope:

- third-party infrastructure not controlled by the project;
- attacks requiring control of a reporter's own node, host, or a non-default deployment;
- public devnet operational-host issues that do not indicate a vulnerability in Twilight
  Core;
- generic denial-of-service against a single unhardened node where no protocol or
  implementation vulnerability is demonstrated;
- social engineering;
- spam, phishing, or abuse reports unrelated to this codebase;
- issues already tracked publicly.

If uncertain, report privately rather than opening a public issue.

## Supported versions

Twilight Core is pre-1.0 and under active development. Security fixes target the latest
`main`, which is the trunk; there is no long-lived integration branch. A formal
supported-version matrix will accompany the first tagged release line.

## Audits and bounty

Twilight Core undergoes continuous internal review, including automated CI and multi-model
adversarial review (see [`REVIEW.md`](REVIEW.md)). This review process is **not** a
substitute for independent security assessment.

An independent third-party security audit and a bug-bounty program are planned before
mainnet. This section will link the audit report, bounty scope, severity framework, and
reward terms when available.
