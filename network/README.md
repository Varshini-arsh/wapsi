# WAPSI Network

## Target platform: Drunix (primary)

Drunix (github.com/npci/drunix) is NPCI's open-source, enterprise-grade DLT —
an enhanced fork of Hyperledger Fabric, **backwards compatible with Fabric
v2.5.x**. The WAPSI chaincode is written against the standard Fabric
`contractapi`, so it is designed to deploy on Drunix without chaincode changes;
the Drunix network path still requires validation in a Drunix environment.

Drunix features relevant to WAPSI:

| Drunix feature | What WAPSI uses it for |
|---|---|
| Org MSP identities & endorsement policies | Only a bank can attest its own hops; LEA co-signs liens |
| Tamper-evident shared ledger | No single bank can rewrite another bank's evidence |
| **YugabyteDB SQL state store** (`-s yugabyte`) | Court-ready SQL reporting over cases/liens |
| Lite Peer / Committing Peer split | Scales endorsement-heavy attestation bursts during fraud waves |
| Private data collections (KeyDB transient store) | Personal data stays off the public channel view |

## Orgs (MVP: 4)

- **Org1MSP** — victim's bank (SBI)
- **Org2MSP** — receiving bank (HDFC)
- **Org3MSP** — downstream bank (Axis)
- **Org4MSP** — law enforcement (I4C / police) — referenced as `leaMSP` in chaincode

One channel: `wapsich`.

## Endorsement semantics (enforced in code + policy)

- `AttestHop` → only `srcBank`'s org (checked in code; tighten with a
  chaincode-level endorsement policy per-bank in production).
- `RecordLien` → holding bank **or** LEA, amount capped by `ComputeTrace`.
- `ProposeRestoration` → majority of trail banks + LEA via `ApproveAction`.

## Bring-up

```bash
cd network
./setup.sh                    # Drunix test network (clone + prereq + up + deploy)
FABRIC_FALLBACK=1 ./setup.sh  # identical chaincode on Fabric test-network
```

Requirements: Docker running, git, bash v4+. On Windows, run inside WSL2.

`setup.sh` clones Drunix to `~/.wapsi/drunix`, runs `./network.sh prereq`,
starts the network with `./network.sh up createChannel -c wapsich -ca -s yugabyte`,
and deploys `../chaincode` as chaincode `wapsi`.

> **Drunix gotcha:** in the Drunix test network, peer index **0 = Lite Peer**
> (endorsement) and **1 = Committing Peer** — inverted vs Fabric. When using
> the peer CLI, `setGlobals 1 1` targets the committing peer.

## Fallback note (for judges)

If the demo runs on the Fabric test-network, it is stated in the README:
same chaincode, same APIs — Drunix is a compatible superset of Fabric v2.5.x,
and the Drunix-specific deploy is one `network.sh` flag (`-s yugabyte`).
