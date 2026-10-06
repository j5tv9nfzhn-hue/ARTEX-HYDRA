# Defending Against and Detecting Autonomous AI Attacks

> This document helps **defenders** understand how an **autonomous AI penetration agent** such as ARTEX operates, so that you can build the capability to **detect and block** such attacks. It is not a guide to carrying out attacks. Use everything here only to protect systems you own or have explicit written authorization to test. Probing or attacking someone else's information and communications network without authorization is itself a crime (see the [security and misuse warning in the top-level README](../README.en.md)).
>
> 한국어판: **[자율 AI 공격 방어·탐지 가이드 (defense-ko.md)](defense-ko.md)**.

An autonomous AI attack tool turns a penetration test — once a manual process a single operator ran by hand — into a 24/7 automated process in which an LLM agent **breaks goals down on its own, executes real tools, and accumulates discoveries**. The adversary a defender faces shifts from "one skilled attacker" to "a swarm of agents that never tire and never rest." This guide sets out what that shift demands of detection and response.

---

## 1. How an autonomous AI attack differs from a traditional scanner

A traditional vulnerability scanner (for example a fixed-signature tool) fires a predefined checklist in order and stops. An autonomous agent like ARTEX is structured differently. As described in the [system architecture](../README.en.md#system-architecture), the following elements combine to **run multi-stage attack chains to completion without human intervention**:

- **Role-separated multi-agents.** The work is split across `goals` (which decomposes objectives), a `planner` (the only producer of intents, which decides the next direction), multiple `worker`s (each of which executes one intent with real tools), and a `mainagent` (the human-in-the-loop point). The planner is the sole intent generator, and the workers carry those intents out in parallel.
- **State accumulated in a dual graph.** "What exists" (the asset graph) and "how far it has been tested" (the exploration graph) are built separately and joined by anchors. As a result the attack **deepens incrementally**, revisits the same asset from new angles, and builds each next step on prior observations.
- **An event-driven closed loop.** Every time the graph changes, the planner wakes, assigns the next intent, and the worker's writes trigger the next round in turn. This cycle does not stop until a goal is proven.
- **Stable progression of serial attack chains.** The planner records dependency ordering — "find an injection point → obtain credentials → move laterally → escalate privileges" — once into a shared todolist, and only assigns an intent to the next step once its predecessor is satisfied. So the attack chain runs to completion without derailing, even in a stateless session environment.
- **The LLM varies payloads per context.** Because the tools and payloads it executes are not fixed constants in code but values the LLM generates by reading the situation, the shape of each individual request varies slightly.

### Why this is harder to detect

- **Fixed signatures match poorly.** Because payloads change with context, rules that match a known malicious string exactly (WAF signatures) are easy to slip past.
- **It can proceed slowly, and in human-like bursts.** The agent rests between rounds and changes direction based on findings, so detection based purely on rate ("many requests in a short window") can miss it.
- **Reconnaissance and intrusion run as one flow.** The gap in which a human reviews recon results and then attacks manually days later disappears, so the time from first contact to data exfiltration shrinks dramatically.

### Why it can still be detected — behavior is hard to hide

Static fingerprints (the User-Agent, a specific payload string) can be changed at the operator's discretion. But the **behavioral pattern of an autonomous agent** is the essence of the attack and is hard to change. Sections 2 through 4 below weight this behavior-based view.

- A single source (or a small set of rotating sources) **chains requests of several different characters** (reconnaissance → enumeration → authentication attempts → exploitation) **within a coherent session**.
- Exploration that **continues without pause** even during hours when a human would stop out of fatigue.
- Adaptive, non-random progression that **systematically mutates the next request** using hints from failed responses.

---

## 2. Fingerprints a defender can observe (IoCs and signatures)

Split the fingerprints into two views. **(a) The target (victim) view** — what you can see in ARTEX traffic aimed at your system. **(b) The operator/forensic view** — what you can see on a host where ARTEX actually ran (or on a compromised relay). It is important not to mix the two. The static fingerprints visible from the target side are limited; the behavioral fingerprints are the core.

### (a) Target view — traffic aimed at your system

- **The enrichment lookup User-Agent `artex-enrich/1.0`.** When ARTEX automatically enriches an asset (DNS and HTTP checks), it sends a direct `GET` to the target with this User-Agent (`enrich/enrich.go`). This path is generated by ARTEX itself, independent of the LLM, and is characterized by **not following redirects, disabling keep-alive, and reading only the start of the response to extract the `<title>`**. The default concurrency is 4. So when lookups with the `artex-enrich/1.0` UA that **open a short-lived connection, issue a single GET, read only the title, and disconnect** arrive at several assets at once, they strongly suggest ARTEX-family enrichment traffic. However, the operator can change the UA, so **its absence does not mean safety**.
- **The actual attack traffic follows the tool's default fingerprint.** The worker sends requests through the real tools it runs (external tools executed via Bash, plus HTTP). To route this traffic through its own recording proxy, ARTEX injects `HTTP_PROXY` and the proxy CA path into the subprocess environment variables — but it **does not force an ARTEX-specific User-Agent onto attack traffic**. So the User-Agent and headers the target sees are **the defaults of whatever tool ran at that moment** (the default UA of the various command-line tools). If the operator did not customize it, a common automation-tool fingerprint remains; if they did customize it, the traffic may be disguised to look like a normal browser. Therefore, **do not rely on single-UA matching; combine it with behavior-based detection**.
- **There is no built-in rate limit.** ARTEX itself has no target-traffic rate limiting, and the request rate is decided by the external tools the LLM drives. Instead, by default 3 workers per task run in parallel, so **several intents may proceed against one target simultaneously**. That means the attack can appear as a "slow single session" or as "multiple angles running at once," so a single fixed threshold is hard to catch it with.
- **Behavioral signatures (most important).** The **co-occurrence** of the patterns below points to an autonomous agent:
  - From one source (or a small set of rotating sources), **reconnaissance → directory/endpoint enumeration → parameter probing → authentication/injection attempts chain at short intervals**.
  - Consecutive requests to the same endpoint that **mutate systematically in response to the status code and length** of the reply (adaptive, not random fuzzing).
  - A session that **continues without pause for long stretches**, outside normal human operating hours.
  - Persistence that keeps trying **bypass variations** even after failures (401/403/429) instead of stopping.

### (b) Operator/forensic view — a host where ARTEX ran

Use these when, during an intrusion investigation, you look for traces of ARTEX installed and run on a relay/transit host.

- **Default listening port `:8787`.** This is the default HTTP listening address of the ARTEX server (`cmd/artex/main.go`, changeable with `--addr`). If an internal host is serving the management UI (dashboard, tasks, asset graph) on this port, that is grounds to suspect an ARTEX instance.
- **The recording MITM proxy `127.0.0.1:8788`.** This is the default address of the local proxy that intercepts and records every worker Bash/HTTP execution end to end (the default of `--proxy` in `cmd/artex/main.go`, loopback only). Because it generates its own CA to decrypt and record TLS (`mitmproxy-ca-cert.pem`), two clues help: whether the host carries **a trusted CA certificate installed by ARTEX**, and whether there are traces of `HTTP_PROXY` and proxy-CA-path environment variables being injected into subprocesses.
- **The self-update callback `artex-selfupdate`.** This is the User-Agent used when the self-updater queries GitHub releases (`selfupdate/`). If egress logs show requests leaving for a code-repository host with this UA, that suggests the presence of an ARTEX binary.
- **The dual graph in PostgreSQL.** A database with tables such as `exploration_nodes`, `assets`, `companies`, and `activity`, plus an `agent_prompts` seed, is characteristic of the ARTEX data store.
- **DB-backed regex approval rules and audit log.** The intercept rules that evaluate tool calls are stored in the DB and evaluated by priority as regular expressions (`intercept/`). Blocked calls are recorded in the audit log (`GET /api/audit`) together with a control framing that begins with `【ARTEX 平台管控·非目标防御】` (a "platform control / non-target defense" framing). So if this string appears in a compromised host's audit records, it supports the conclusion that ARTEX ran there.
- **Destructive-command hunting indicators.** The command patterns ARTEX's own guard embeds as block targets are, in effect, the mirror image of the command family an autonomous agent **might attempt**. In host command auditing, treat the following as hunting indicators: `rm -rf`, `mkfs`, `dd if=`, `shred`/`wipe`, SQL `DROP DATABASE`/`DROP TABLE`/`TRUNCATE`, MongoDB `drop`/`dropDatabase`, Redis `FLUSHALL`/`FLUSHDB`, `-X DELETE` on `curl`/`wget`, and data-exfiltration pipes of the form `curl … | nc …`.

> In short: **anchor target-side defense on behavioral fingerprints, and use static UAs (`artex-enrich/1.0` and the like) only as supporting clues.** The operator/forensic fingerprints (`:8787`, `127.0.0.1:8788`, `artex-selfupdate`, the DB schema, the audit-log framing) are valid **when investigating a compromised transit host**.

### Why IP-address blocking is a weak first line of defense

When a security incident becomes known, posts that say "here is a shared list of attacker IPs — block them at your firewall" commonly circulate on social media and community forums. Whatever the good intent of those sharing them, **we do not recommend dropping an unofficial IP list of unclear provenance straight into your block rules** — and this holds especially against autonomous AI attacks.

- **The source is hard to verify.** For an unofficial list posted by an individual, there is no way to confirm who collected it or on what basis, and no way to filter out the IPs unrelated to the incident that may be mixed in.
- **It ages quickly.** An autonomous agent constantly rotates its origin IP through VPNs, cloud instances, and hijacked relay servers (the "small number of rotating sources" in (a) above). An attack IP observed yesterday was likely already discarded today, so blocking the list still lets the attacker return from a different IP.
- **The false-blocking risk is high.** If the list mixes in shared ranges, CDNs, or legitimate cloud IPs, the moment you block them you also cut off healthy customer traffic or internal services. Combined with automatic blocking (section 6 below), the false-block damage spreads even faster.

This does not mean IP blocking is useless. It becomes meaningful **when you receive official indicators of compromise (IoCs) from a response agency or trusted threat intelligence and apply them after reviewing their validity window and false-positive potential.** But blocking a single IP line is only a stopgap that chases a rotating origin; what lasts is the **behavior** that is hard to change (the behavior-based detection in sections 2–4) and the **reduction of attack surface** (the hardening in sections 3 and 5 — trimming exposed assets, patching, MFA). This guide's premise — fingerprints can change, but behavior is hard to hide — applies here too.

---

## 3. Entry points attackers target, and hardening

An autonomous agent targets the **same weaknesses** a human attacker does, but repeats them faster and more relentlessly. Below are the priority hardening points from a defender's view.

### 3.1 Auxiliary authentication and identity-verification flows

**Authentication and identity-verification flows attached through a different path than the main service** — add-on services, partner channels, recruitment channels — are often loosely validated and become bypass targets. An autonomous agent enumerates these paths automatically and reads response differences to find bypass conditions systematically.

- Unify identity-verification and authentication steps **to the same strength as the main service**, and audit every auxiliary path that could be bypassed.
- **Re-verify authentication state transitions** (unauthenticated → authenticated, user → privileged) **on the server**, and do not blindly trust the trust markers the client sends (cookies, headers, parameters).
- Check the **lifetime, reuse, and guessability** of identity-verification tokens and one-time codes.

### 3.2 API authentication and authorization (IDOR and privilege escalation)

- Enforce a **server-side ownership/authorization check** on every object access (block IDOR, where changing only an identifier opens someone else's resource).
- Enumerate horizontal and vertical privilege-escalation paths yourself. Because an autonomous agent mechanically increments and decrements identifiers and tries them in bulk, it quickly finds **holes that a single manual test missed**.

### 3.3 Credential stuffing

Attacks that replay leaked ID/password lists are amplified by an autonomous agent through **speed and distribution**.

- Apply **adaptive rate limiting** (based on IP, account, device, and behavior) to login and identity-verification endpoints.
- Enforce **multi-factor authentication (MFA)** on sensitive operations. Even if stuffing lands a correct password, the second factor blocks it.
- Block preemptively with **compromised-credential detection** (checking against known leak lists; anomalous login location/velocity).
- Alert on **distribution shifts** in login failures and successes (a sudden low-and-wide attempt).

### 3.4 Session, token, and secret management

- Minimize the **scope, lifetime, and renewal** of session tokens, and re-authenticate at every sensitive transition.
- **Do not expose** API keys or internal tokens in responses, logs, or error messages (an autonomous agent actively harvests clues from error responses).

---

## 4. Detection rules and log patterns (practical)

Written as product-independent **pseudo-rules**. Translate them into your own WAF/IPS/SIEM syntax. The rules below that rest on static fingerprints are shipped as ready-to-deploy [Sigma rules (`detections/sigma/`)](../detections/). The core behavior and correlation detection (4.1 and 4.2) does not reduce to a single rule either, but the behavioral indicators grounded in ARTEX's source are shipped as deployable [Sigma correlation rules (`detections/sigma/correlation/`)](../detections/) — enrichment velocity, enrichment fan-out, guard-block burst, and the guard marker co-occurring with a destructive command on one host. The pure web multi-stage correlation (enumerate → probe → authenticate) still needs base rules specific to your environment, because the attack traffic carries no ARTEX-unique User-Agent; build that one in your own SIEM from the description below. The enrich prober User-Agent — observable at the network layer where traffic is plaintext HTTP or inspected at a TLS-terminating point — is also shipped as [Suricata rules (`detections/suricata/`)](../detections/suricata/).

### 4.1 WAF/IPS (behavior-based)

- When **requests of different characters** from a single source (a low share of static-resource requests, a high share of enumeration, parameter probing, and authentication attempts) **continue as one session**, raise the score.
- Weight **consecutive requests that mutate in response** to status code and body length (high entropy but an adaptive, non-random pattern).
- Tag a known automation UA such as `artex-enrich/1.0` as **immediately high-risk**, but do not read the absence of a UA as safety.

### 4.2 SIEM correlation rules

- **Same-source multi-stage correlation:** when (a) directory/endpoint enumeration, (b) parameter probing, and (c) authentication/injection attempts are **all observed within a short window** from the same IP/ASN/session, raise an "autonomous attack suspected" alert.
- **Time-of-day anomaly:** a single session that **continues without pause for a long stretch**, outside the service's normal traffic distribution.
- **Persistence after failure:** a source that receives 403/429 and keeps going with **bypass variations** instead of stopping.

### 4.3 Authentication logs

- **Sudden shifts in the login failure rate** per account/IP, **slow distributed attempts** spread across a wide range of accounts (characteristic of stuffing), and an **abnormal failure-to-success transition speed**.
- **Enumeration-style access** to identity-verification and one-time-code endpoints.

### 4.4 Egress and forensics

- Requests leaving an internal host for a code-repository host with the `artex-selfupdate` UA.
- Processes bound internally to `:8787` (the management UI) or `127.0.0.1:8788` (the recording proxy).
- A DNS/HTTP enrichment pattern that looks up a large number of external assets in a short time with the `artex-enrich/1.0` UA.

---

## 5. Hardening checklist

Summarized so a defending team can check it right away.

- [ ] Applied **adaptive rate limiting** (IP, account, device, behavior) to login, identity-verification, and sensitive APIs.
- [ ] Enforces **MFA** on sensitive operations.
- [ ] Has a **server-side ownership/authorization check** on every object access (blocks IDOR).
- [ ] **Re-verifies authentication state transitions on the server** and does not blindly trust client trust markers.
- [ ] Unified the identity-verification strength of **auxiliary/partner/recruitment channels** with the main service.
- [ ] Operates **compromised-credential detection/matching** for leaked credentials.
- [ ] Runs the WAF in **behavior-based mode** and does not rely on fixed signatures alone.
- [ ] **Does not apply circulating unofficial IP block lists as-is**, and instead takes official indicators of compromise (IoCs) from a trusted source and applies them after reviewing their validity window and false-blocking risk.
- [ ] Added a **same-source multi-stage correlation rule** to the SIEM.
- [ ] **Retains authentication, access, and egress logs for a sufficient period** (autonomous attacks are fast, so after-the-fact tracing material matters).
- [ ] Reduced the blast radius of lateral movement and privilege escalation with network **segmentation**.
- [ ] **Does not expose** secrets (keys, tokens) in responses, logs, or error messages.
- [ ] Prepared **automatic blocking/isolation** response (waiting only on human approval cannot keep up with autonomous attack speed).

---

## 6. Incident response summary

The defining trait of an autonomous AI attack is **speed**. An agent can run to completion, in a far shorter time, the intrusion and exfiltration that would take a person a year. Design your response on the premise of this speed.

- **Automatic blocking first.** Set up measures such as isolating a suspect source, invalidating sessions, and sharply cutting the rate so they can **fire automatically** before human approval. Binding everything to a human-approval loop cannot keep up with the attack's speed.
- **Decide in advance which logs to retain.** Authentication logs, access logs (including request bodies within the extent you can capture), egress logs, DNS queries. Autonomous attacks accumulate traces quickly, so this material is what you need to reconstruct the attack chain afterward.
- **Track the scope of compromise asset by asset.** Because the attack spreads along the asset graph, you must reconstruct the **entire path** from the initial entry asset through lateral movement and privilege escalation to prevent re-intrusion.

---

## References

- Upstream project: [Autumn-27/ARTEX](https://github.com/Autumn-27/ARTEX) (AGPL-3.0). This document is the defensive material of its Korean-edition repository.
- The top-level [README security and misuse warning, scope of use, and legal notice](../README.en.md).
- This edition is localized for Korea; where personal data is involved, Korean law (the Network Act and the Personal Information Protection Act) applies. Unauthorized testing is a crime in most jurisdictions regardless — always secure written authorization and an agreed scope first.
- Standard references for general web-security hardening: OWASP Top 10, OWASP ASVS (Application Security Verification Standard), OWASP API Security Top 10.

> This guide is continually expanded to support defense and detection capability. Suggestions for additional detection rules or hardening items are welcome as repository issues.
