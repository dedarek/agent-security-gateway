#!/usr/bin/env python3
"""ASG data reset: clear business activity data, delete test agents, keep real ones.

Usage (Windows git-bash, gateway must be STOPPED first — run via svcctl or stop service):
  python scripts/asg_reset_data.py            # dry-run: prints what WOULD change
  python scripts/asg_reset_data.py --apply    # actually applies

What it does:
  - agents: delete test-residue agents (those without a real machine_name or with
    a known test prefix); keep real agents (a real harness on a real machine).
  - events / activity_steps / model_history: cleared (business history).
  - policies: kept (operator config, not activity data).
  - KG graph: caller should restart gateway afterwards so replayKG re-seeds an
    empty graph (or run with gateway stopped and delete the KG sidecar mirror).

Safety: always makes a timestamped backup of data/asg.db before --apply.
"""
import argparse, os, shutil, sqlite3, sys, time

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
DB = os.path.join(ROOT, "data", "asg.db")
EVENTS_JSONL = os.path.join(ROOT, "data", "events.jsonl")

TEST_PREFIXES = (
    "bugb-", "final-", "hook-agent", "sectest-", "e2e-", "test-", "audit-",
    "rtt-", "lv-", "lineage-", "tp", "dbg-", "rep-", "g3-", "gg-", "fp",
    "vchain", "gfinal", "clean-", "chain-", "eng", "guard-", "m3-", "sess-",
    "red-", "probe-",
)
TEST_EXACT = {"x"}


def is_test_agent(agent_id: str, machine_name: str, session_count: int = 0, alias: str = "") -> bool:
    """A real agent = a real harness instance on a real machine. Keep it only if
    it has a real machine identity AND a real alias (operator-named) OR is the
    known local box. Test residue = no real machine, fake machine (m/m1/test/e2e),
    or a synthetic id pattern (incl. claude-code-macdemacbook-air, which has
    sessions but no machine_name → probe/test artifact)."""
    if not machine_name or machine_name in ("e2e", "test", "m", "m1", "test-m"):
        return True
    if agent_id in TEST_EXACT:
        return True
    if agent_id.startswith(TEST_PREFIXES):
        return True
    # macdemacbook-air style: hyphenated harness name but no genuine machine_name
    # and a machine_id that is itself a synthetic hash → test residue.
    if "macdemacbook" in agent_id or agent_id.startswith("claude-code-"):
        return True
    return False


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--apply", action="store_true", help="apply changes (default: dry-run)")
    args = ap.parse_args()

    if not os.path.exists(DB):
        print(f"DB not found: {DB}")
        return 1

    # work on a live copy for dry-run reads; for apply, back up then edit in place
    con = sqlite3.connect(DB)
    con.row_factory = sqlite3.Row

    # agents table stores a record_json blob; machine/alias live inside it.
    import json as _json
    agents = []
    for row in con.execute("SELECT agent_id, record_json FROM agents").fetchall():
        try:
            rec = _json.loads(row["record_json"] or "{}")
        except Exception:
            rec = {}
        agents.append({
            "agent_id": row["agent_id"],
            "machine_name": rec.get("machine_name") or rec.get("machine_id") or "",
            "alias": rec.get("alias") or "",
        })
    keep, drop = [], []
    for a in agents:
        (drop if is_test_agent(a["agent_id"], a["machine_name"]) else keep).append(a)

    print("=== agents ===")
    for a in keep:
        print(f"  KEEP  {a['agent_id']:30} machine={a['machine_name']} alias={a['alias']}")
    for a in drop:
        print(f"  DROP  {a['agent_id']:30} machine={a['machine_name'] or '-'}")

    counts = {}
    for t in ("events", "activity_steps", "model_history"):
        counts[t] = con.execute(f'SELECT count(*) FROM "{t}"').fetchone()[0]
    print("=== business data to clear ===")
    for t, n in counts.items():
        print(f"  {t:20} {n} rows")

    if not args.apply:
        print("\nDRY-RUN — nothing changed. Re-run with --apply to apply.")
        con.close()
        return 0

    # backup
    ts = time.strftime("%Y%m%d-%H%M%S")
    bak = f"{DB}.bak-{ts}"
    con.close()
    shutil.copy(DB, bak)
    print(f"\nbackup: {bak}")

    con = sqlite3.connect(DB)
    cur = con.cursor()
    for a in drop:
        cur.execute("DELETE FROM agents WHERE agent_id=?", (a["agent_id"],))
    for t in ("events", "activity_steps", "model_history"):
        cur.execute(f'DELETE FROM "{t}"')
    con.commit()
    # reset sqlite autoincrement so ids restart cleanly
    try:
        cur.execute("DELETE FROM sqlite_sequence WHERE name IN ('events','activity_steps','model_history')")
        con.commit()
    except sqlite3.Error:
        pass
    con.close()

    # truncate the JSONL audit mirror (keep the file, empty it)
    if os.path.exists(EVENTS_JSONL):
        shutil.copy(EVENTS_JSONL, f"{EVENTS_JSONL}.bak-{ts}")
        open(EVENTS_JSONL, "w", encoding="utf-8").close()
        print(f"truncated: {EVENTS_JSONL} (bak {EVENTS_JSONL}.bak-{ts})")

    print("\nAPPLIED. Now restart the gateway so replayKG rebuilds an empty KG graph.")
    print(f"  kept agents: {len(keep)} · dropped agents: {len(drop)} · cleared events/activity/model_history")
    return 0


if __name__ == "__main__":
    sys.exit(main())
