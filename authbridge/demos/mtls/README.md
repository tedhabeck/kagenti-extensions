# mTLS demo — agent-to-agent encryption via SPIRE X.509 SVIDs

> **Status (2026-05-27).** The **envoy-sidecar variant**
> (`make demo-mtls-envoy*`) is verified end-to-end on a kind +
> kagenti + SPIRE cluster; see the
> [envoy-sidecar variant](#envoy-sidecar-variant) section below.
> The **proxy-sidecar variant** (`make demo-mtls`) currently fails
> at the verification step: the kagenti-operator's controller
> reconciles per-agent `authbridge-config-*` ConfigMaps and reverts
> the `mtls:` block that `patch-mtls-config.sh` writes, before the
> restarted pod can mount it. Both demos are blocked on the same
> upstream operator bug — see [Operator bugs uncovered](#operator-bugs-uncovered).
> The Pod manifests in `k8s/{caller,callee}.yaml` were also missing
> the `kagenti.io/type: agent` label that the operator's
> mutating-webhook objectSelector requires; both demos' manifests
> have been updated to include it.

Two pods that talk to each other through their authbridge sidecars,
configured with `mtls.mode: strict`. Confirms end-to-end:

- authbridge's `spiffe.Provider` mirror writes `/opt/svid.pem` /
  `_key.pem` / `_bundle.pem` on both pods on every rotation; the files
  exist for external readers (e2e probes, debugging, future Envoy
  filesystem SDS).
- For the listener itself, authbridge reads X.509 SVIDs in-memory via
  `spiffe.X509Source` (no per-handshake file I/O) and uses them for
  both inbound (reverse proxy) and outbound (forward proxy) mTLS.
- The wire between the two pods is encrypted: a `tcpdump` on the
  cluster network shows no plaintext HTTP request lines on TCP port
  8080.
- abctl shows the negotiated TLS version + peer SPIFFE ID per session
  event in the detail pane.

This is intentionally minimal. It doesn't exercise IBAC, weather, or
the token-exchange flows — it just proves the mTLS layer is real.

## Prerequisites

1. **A kagenti install on a kind cluster** with SPIRE enabled.
2. **kubectl, kind, podman (or docker)** on PATH.

## Quick start

```sh
cd authbridge/demos/mtls

make demo-mtls          # deploys caller + callee, both with mtls.mode: strict
                        # waits for ready, runs an end-to-end request,
                        # asserts the wire was encrypted via tcpdump

make demo-mtls-permissive
                        # same shape but with mtls.mode: permissive
                        # sends a plaintext curl from outside the mesh,
                        # asserts the request is served (proving permissive
                        # serves both)

make demo-mtls-strict-rejects-plain
                        # hits a strict callee with plain HTTP and asserts
                        # the connection is closed (proving strict actually
                        # enforces)

make undeploy           # cleanup
```

## Verification recipe

The `make demo-mtls` target does:

1. Deploys caller + callee Pods to `team1` with operator-injected
   sidecars and SPIRE.
2. Patches the operator-rendered `authbridge-config-{caller,callee}`
   ConfigMaps to add `mtls: { mode: strict }`.
3. Restarts the pods so the new config takes effect (mTLS config
   requires pod restart per the framework hot-reload boundary).
4. Triggers a request from caller → callee through the kagenti UI
   flow (caller's outbound forward proxy → callee's inbound reverse
   proxy).
5. Captures cluster traffic via `tcpdump -i any -n -A 'tcp port 8080'`
   inside the kind cluster's network namespace. Asserts no plaintext
   HTTP/1.1 request lines appear in captured packets.
6. Greps both pods' logs for the startup line confirming mTLS is
   enabled (`"mTLS enabled" mode=strict`).
7. Reports the negotiated TLS version + peer SPIFFE ID from the
   session API.

## What this demo doesn't cover

- **Permissive vs strict rollout**: covered by `make demo-mtls-permissive`
  and `make demo-mtls-strict-rejects-plain`.
- **Cert rotation under load**: tested via unit tests in
  `authlib/spiffe/x509source_test.go`.
- **kagenti UI integration**: this demo is intentionally pre-UI —
  if you want to chat with an mTLS-protected agent, run any other
  kagenti demo with `mtls: { mode: strict }` added to its config.
- **Ambient mesh interaction**: when ambient is on, ztunnel handles
  the cross-pod hop and authbridge's mTLS is redundant. This demo
  doesn't run ambient. Phase 5 will add ambient detection so the
  redundancy is auto-dropped.

## envoy-sidecar variant

The targets above (`make demo-mtls`, `make demo-mtls-permissive`, etc.)
exercise mTLS in **proxy-sidecar** mode, where authbridge itself is
the listener and the byte-peek + dial logic lives in Go
(`authlib/listener/internal/tlssniff` for inbound,
`authlib/listener/forwardproxy` for outbound).

A parallel set of targets exercises **envoy-sidecar** mode, where
Envoy is the listener and mTLS is configured at the data-plane level
via `DownstreamTlsContext` / `UpstreamTlsContext` reading
`/opt/svid*.pem`:

```sh
make demo-mtls-envoy                          # strict: deploy + verify
make demo-mtls-envoy-permissive               # permissive: deploy + verify plaintext callers served
make demo-mtls-envoy-strict-rejects-plain     # strict: verify plaintext rejected
make undeploy-envoy                           # tear down + restore namespace envoy-config
```

### Design

| `mtlsMode` | Inbound (peer-facing :15124) | Outbound (app-egress :15123) |
| --- | --- | --- |
| `disabled` (default) | plaintext | plaintext |
| `permissive` | `tls_inspector` + two filter chains: TLS chain terminates mTLS, raw_buffer chain accepts plaintext | **plaintext** — no TLS-wrap attempt |
| `strict` | `tls_inspector` + single TLS chain; plaintext rejected at chain match | `UpstreamTlsContext` on the `original_destination` cluster: TLS-or-fail, blanket |

Inbound is byte-identical to proxy-sidecar's `tlssniff.Listener`
(same protocol semantics, just expressed as Envoy filter chains —
matches Istio's PERMISSIVE/STRICT inbound exactly).

Outbound is **Istio-shaped**: there is no per-connection
try-then-fallback (Envoy has no native primitive for it, and Istio
itself doesn't do it — it relies on Pilot pre-deciding mesh
membership). Blanket TLS in strict mode is practical for the same
reason proxy-sidecar's strict mode is: outbound calls that need
plaintext (Keycloak / JWKS / external HTTPS) never hit the listener:

1. Plugin outbound (token-exchange, jwt-validation talking to
   Keycloak / JWKS) uses Go's `net/http` directly — bypasses Envoy.
2. External HTTPS from the app — `proxy-init`'s iptables redirects
   only plaintext HTTP to the outbound listener; HTTPS bypasses it.
3. HTTP from the app to peer agents — what gets TLS-wrapped. Peer's
   inbound listener terminates. Works.

### Behavioral gap vs proxy-sidecar

In proxy-sidecar, a *permissive* caller can reach a *strict* peer
because its outbound dialer tries TLS first per-connection and the
handshake succeeds. In envoy-sidecar permissive, outbound is
plaintext, so a permissive envoy-sidecar caller calling a strict
peer **fails**. Mixed-mode deployments need both ends compatible
(both strict, both permissive, or one strict + the other permissive
on inbound only). The framework documentation in
[`authbridge/CLAUDE.md`](../../CLAUDE.md#top-level-mtls-configuration)
calls this out.

### How the demo wires mTLS

The demo is fully **hand-crafted** — no `kagenti.io/inject` label,
no operator pod injection. This sidesteps both
[operator bugs](#operator-bugs-uncovered): no RO `/opt` mount because
we control the volumes, no per-agent CM reconciliation because there
is no per-agent CM (the demo brings its own `envoy-config-mtls-active`
and `authbridge-runtime-mtls` CMs that the operator doesn't touch).

The demo pods bring their own:

- ServiceAccount for SPIFFE identity (auto-registered via the
  cluster's default `ClusterSPIFFEID` template
  `spiffe://<trust-domain>/ns/<ns>/sa/<sa>`).
- `authbridge-runtime-mtls` ConfigMap — minimal authbridge config
  with a no-op pipeline (jwt-validation + token-exchange registered
  but never invoked, since our Envoy config has no ext_proc filter).
- `envoy-config-mtls-active` ConfigMap — populated by
  `swap-envoy-config.sh` from one of the per-mode variants.
- emptyDir for `/opt` (RW), CSI mount for `/spiffe-workload-api`.

The demo's Envoy config uses **STATIC clusters** with explicit
upstream addresses (`callee_cluster` points at
`mtls-callee-envoy.team1.svc.cluster.local:8080`) rather than
`ORIGINAL_DST` + iptables. That lets the demo skip the proxy-init
container and HTTP_PROXY-route from the demo-app to the local
Envoy outbound listener — same pattern the proxy-sidecar mTLS
demo uses for its forward proxy on `:8081`.

Production traffic uses ORIGINAL_DST + iptables — that's a
deployment-shape concern, not a mTLS-design concern. Reusing the
same TLS blocks (`tls_inspector`, `DownstreamTlsContext`, filter
chain match, `UpstreamTlsContext`) in the operator's
`envoy.yaml.tmpl` with ORIGINAL_DST is what the kagenti-operator
follow-up PR does. The follow-up also fixes the RO `/opt` mount
and per-agent CM rendering; once it lands, the user sets
`mtlsMode: strict` on the AgentRuntime CR (or in the kagenti UI)
and the operator wires up the same Envoy YAML this demo ships by
hand.

## Operator bugs uncovered

Running both variants against a real cluster surfaced three
kagenti-operator issues. Two of them block the demos today; the
third is a Pod-spec selector mismatch this PR works around. The
kagenti-operator follow-up PR is expected to fix #1 and #2; #3 is
already fixed by the manifest updates in this PR.

### #1 — `/opt` mounted `readOnly:true` on envoy-sidecar's `envoy-proxy` container

Verified empirically:
- proxy-sidecar `authbridge-proxy` container mounts `svid-output`
  at `/opt` with `readOnly` defaulted (false → RW).
- envoy-sidecar `envoy-proxy` container mounts the same volume at
  `/opt` with `readOnly: true`.

The in-process spiffe Provider mirror writes `/opt/svid.pem`,
`/opt/svid_key.pem`, `/opt/svid_bundle.pem` on every SPIRE rotation;
under the RO mount the mirror logs:

```text
spiffe.mirror: initial x509 write
  err="write svid.pem: atomicWrite: create temp in /opt: open /opt/.tmp-svid.pem.4183688747: read-only file system"
```

Envoy then refuses to boot (`Invalid path: /opt/svid_bundle.pem`)
because the file-based `DownstreamTlsContext` / `UpstreamTlsContext`
references can't resolve. **Affects: envoy-sidecar mode only.** The
fix is to flip the readOnly bit on the `svid-output` volumeMount in
the envoy-sidecar branch of `BuildEnvoyProxyContainerWithSpireOption`
in `kagenti-operator/internal/webhook/injector/container_builder.go`.

### #2 — Operator controller reconciles per-agent CM, erases user-added fields

`patch-mtls-config.sh` writes `mtls: { mode: strict }` into the
per-agent `authbridge-config-<name>` ConfigMap. Some operator-side
component (admission webhook on pod creation, or a reconciler watching
the CM) re-templates the CM back to its operator-rendered shape
*before* the pod's kubelet-mounted view picks up the patch:

```console
$ kubectl -n team1 get cm authbridge-config-mtls-caller \
    -o jsonpath='{.metadata.annotations.kubectl\.kubernetes\.io/last-applied-configuration}' \
    | jq -r '.data."config.yaml"' | tail -3
spiffe: {}
mtls:
  mode: strict                 ← what patch-mtls-config.sh wrote

$ kubectl -n team1 get cm authbridge-config-mtls-caller \
    -o jsonpath='{.data.config\.yaml}' | tail -3
      name: token-exchange
spiffe: {}                     ← what the operator left after reconcile
                                  (NO mtls block)

$ kubectl -n team1 logs deploy/mtls-caller -c authbridge-proxy | grep mtls
"mTLS disabled (no mtls block in config)"
```

Same pattern applied to my `spiffe.mirror_dir: /shared` workaround
during envoy-sidecar testing. **Affects: BOTH proxy-sidecar (`make
demo-mtls`) and envoy-sidecar variants of this demo.** Either the
reconciler should preserve user-added fields it doesn't manage, or
the canonical surface should be `Spec.MTLSMode` on the AgentRuntime
CR with the operator rendering the resulting `mtls:` block — i.e.,
the operator follow-up PR's plan.

### #3 — Pod selector mismatch (workaround in this PR)

The kagenti-operator's mutating-webhook `objectSelector` requires
`kagenti.io/type` in `[agent, tool]`. The demos' Pod templates didn't
set this label, so no operator injection happened (no authbridge
sidecar, no per-agent CM created). Either the selector tightened
recently or the demos haven't been re-run against a current operator.

This PR adds `kagenti.io/type: agent` to all four demo Pod templates
(`caller.yaml`, `callee.yaml`, `caller-envoy.yaml`, `callee-envoy.yaml`)
and an explicit `runAsUser: 100` on the `demo-app` container to
satisfy `runAsNonRoot: true` against the
`curlimages/curl:latest` image (which now USERs as the non-numeric
`curl_user`, tripping the kubelet's nonRoot check). With these in
place the proxy-sidecar demo's Pods at least admit and roll out;
verification still fails on issue #2 above.
