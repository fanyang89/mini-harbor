#!/usr/bin/env python3
"""Exercise rqlite HA using disposable Docker resources and organization SQL."""

import json
import subprocess
import time
import urllib.error
import urllib.request
import uuid


IMAGE = "rqlite/rqlite:10.6.0@sha256:a63af1b16b051ed7d8c81b57b4d704d47faaf1c0002f60c8b6bb06a41535644a"
HTTP = urllib.request.build_opener(urllib.request.ProxyHandler({}))
INSERT = "INSERT INTO organizations VALUES (?, ?, ?, ?, ?)"
SELECT = "SELECT * FROM organizations ORDER BY id"
SCHEMA = """CREATE TABLE IF NOT EXISTS organizations (
    id TEXT PRIMARY KEY NOT NULL,
    display_name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
)"""


def docker(*args, check=True):
    result = subprocess.run(
        ["docker", *args],
        capture_output=True,
        text=True,
        timeout=180,
        check=False,
    )
    if check and result.returncode:
        raise RuntimeError(f"docker {' '.join(args)}: {result.stderr}")
    if args[0] == "logs":
        return result.stdout + result.stderr
    return result.stdout.strip()


def require(condition, message):
    if not condition:
        raise AssertionError(message)


def wait_for(description, action, timeout=45):
    deadline = time.monotonic() + timeout
    last = None
    while time.monotonic() < deadline:
        try:
            result = action()
            if result:
                return result
        except (OSError, ValueError, RuntimeError) as error:
            last = error
        time.sleep(0.2)
    raise RuntimeError(f"Timed out waiting for {description}: {last}")


def request(url, path, statements=None):
    data = None if statements is None else json.dumps(statements).encode()
    req = urllib.request.Request(
        url + path,
        data=data,
        headers={"Content-Type": "application/json"},
    )
    with HTTP.open(req, timeout=5) as response:
        return json.load(response)


def errors(body):
    return [
        item
        for item in [body, *body.get("results", [])]
        if item.get("error") or item.get("error_v2")
    ]


def sql(url, statements, read=False, local=False):
    if read:
        path = (
            "/db/query?level=none"
            if local
            else ("/db/query?level=linearizable&linearizable_timeout=2s&timeout=2s")
        )
    else:
        path = "/db/execute?transaction&timeout=2s"
    body = request(url, path, statements)
    if errors(body):
        raise RuntimeError(json.dumps(body))
    require(len(body.get("results", [])) == len(statements), str(body))
    return body["results"]


def rows(url, local=False):
    return sql(url, [SELECT], read=True, local=local)[0].get("values", [])


def main():
    prefix = "mini-harbor-rqlite-poc-" + uuid.uuid4().hex[:10]
    nodes = [prefix + f"-n{i}" for i in range(3)]
    created = []
    network_created = False
    urls = {}
    timestamp = "2026-11-09T00:00:00Z"
    expected = [["engineering", "研发", "quote ' and Unicode", timestamp, timestamp]]

    def leader(candidates):
        for node in candidates:
            try:
                status = request(urls[node], "/status")
                if status["store"]["raft"]["state"] == "Leader":
                    return node
            except (OSError, ValueError):
                pass
        return None

    def converged():
        # Local reads prove each replica has applied the data, not forwarded it.
        return all(rows(urls[node], local=True) == expected for node in nodes)

    def refresh_urls():
        # Docker may allocate a new ephemeral host port after container restart.
        for node in nodes:
            urls[node] = "http://" + docker("port", node, "4001/tcp")

    try:
        docker("network", "create", prefix)
        network_created = True
        join = ",".join(f"{node}:4002" for node in nodes)
        for node in nodes:
            docker(
                "run",
                "-d",
                "--name",
                node,
                "--hostname",
                node,
                "--network",
                prefix,
                "-p",
                "127.0.0.1::4001",
                IMAGE,
                "-bootstrap-expect",
                "3",
                "-join",
                join,
            )
            created.append(node)
            port = docker("port", node, "4001/tcp")
            urls[node] = "http://" + port

        first = wait_for("initial leader", lambda: leader(nodes))
        wait_for("quorum readiness", lambda: sql(urls[first], ["SELECT 1"], read=True))
        sql(urls[first], [SCHEMA])
        follower = next(node for node in nodes if node != first)
        sql(urls[follower], [[INSERT, *expected[0]]])
        for node in nodes:
            require(rows(urls[node]) == expected, "Read-after-write mismatch")
        print(
            "PASS: schema, parameterized create, follower forwarding, linearizable reads",
            flush=True,
        )

        duplicate = request(
            urls[follower], "/db/execute?transaction", [[INSERT, *expected[0]]]
        )
        require(bool(errors(duplicate)), "Duplicate ID unexpectedly succeeded")
        rollback_row = ["rollback", "Rollback", "", timestamp, timestamp]
        transaction = request(
            urls[first],
            "/db/execute?transaction",
            [
                [INSERT, *rollback_row],
                [INSERT, *expected[0]],
            ],
        )
        require(bool(errors(transaction)), "Invalid transaction unexpectedly succeeded")
        require(rows(urls[first]) == expected, "Failed transaction was not rolled back")
        print(
            "PASS: duplicate constraint and atomic transaction rollback (HTTP 200 SQL errors)",
            flush=True,
        )

        sql(
            urls[follower],
            [
                [
                    "UPDATE organizations SET display_name=?, updated_at=? WHERE id=?",
                    "Platform",
                    timestamp,
                    "engineering",
                ]
            ],
        )
        expected[0][1] = "Platform"
        sql(urls[first], [[INSERT, *rollback_row]])
        deleted = sql(
            urls[follower], [["DELETE FROM organizations WHERE id=?", "rollback"]]
        )
        require(deleted[0]["rows_affected"] == 1, "Delete did not affect one row")
        require(rows(urls[first]) == expected, "CRUD result mismatch")
        wait_for("initial replica convergence", converged)
        print("PASS: update, delete, list, and local replica convergence", flush=True)

        started = time.monotonic()
        docker("kill", "--signal=KILL", first)
        survivors = [node for node in nodes if node != first]
        second = wait_for("replacement leader", lambda: leader(survivors))
        wait_for("replacement quorum read", lambda: rows(urls[second]) == expected)
        added = ["after-failover", "HA", "", timestamp, timestamp]
        # Do not blindly retry writes whose outcome is unknown.
        sql(urls[second], [[INSERT, *added]])
        expected = sorted([*expected, added])
        require(rows(urls[second]) == expected, "Post-failover write missing")
        print(
            f"PASS: leader SIGKILL, committed data retained, new write in {time.monotonic() - started:.2f}s",
            flush=True,
        )

        docker("kill", "--signal=KILL", second)
        remaining = next(node for node in survivors if node != second)
        # Wait for the surviving follower to recognize loss of leadership.
        wait_for(
            "leader loss",
            lambda: (
                not request(urls[remaining], "/status")["store"]["leader"].get(
                    "node_id"
                )
            ),
        )
        denied = ["without-quorum", "Denied", "", timestamp, timestamp]
        try:
            body = request(
                urls[remaining], "/db/execute?timeout=2s", [[INSERT, *denied]]
            )
        except urllib.error.HTTPError as error:
            require(error.code == 503, f"Unexpected HTTP status: {error.code}")
            error.close()
        else:
            require(bool(errors(body)), "Minority unexpectedly acknowledged a write")
        print("PASS: minority does not acknowledge writes", flush=True)

        docker("start", first, second)
        refresh_urls()
        wait_for("recovered leader", lambda: leader(nodes))
        wait_for("recovered local replicas", converged)
        print(
            "PASS: restarted nodes catch up; minority write absent on every replica",
            flush=True,
        )

        docker("kill", "--signal=KILL", *nodes)
        docker("start", *nodes)
        refresh_urls()
        recovered = wait_for("full restart leader", lambda: leader(nodes))
        wait_for("full restart quorum", lambda: rows(urls[recovered]) == expected)
        wait_for("full restart convergence", converged)
        sql(urls[recovered], [SCHEMA])
        probe = sql(
            urls[recovered], [["DELETE FROM organizations WHERE id=?", "missing"]]
        )
        require(probe[0].get("rows_affected", 0) == 0, "Missing-row delete mismatch")
        print(
            "PASS: full cluster restart preserves data and accepts writes; migration rerun",
            flush=True,
        )
        print("PASS: all rqlite PoC checks", flush=True)
    except BaseException:
        for node in created:
            print(
                f"--- {node} ---\n{docker('logs', '--tail', '30', node, check=False)}",
                flush=True,
            )
        raise
    finally:
        if created:
            docker("rm", "-f", "-v", *created, check=False)
        if network_created:
            docker("network", "rm", prefix, check=False)


if __name__ == "__main__":
    main()
