"""Live compatibility probe against the frozen Python checkout (test databases only)."""
import asyncio
import json
import os
import sys
from datetime import datetime, timezone
from pathlib import Path

from surriti import Surriti
from surriti.driver import SurrealDriver
from surriti.memory_pack import export_group_to_zip, validate_pack_zip, import_group_from_zip


async def main():
    database, action, path = sys.argv[1:]
    if not database.startswith("surriti_go_test_"):
        raise ValueError("bridge refuses non-test databases")
    driver = SurrealDriver(
        url=os.environ["SURRITI_SURREAL_URL"],
        namespace=os.environ.get("SURRITI_SURREAL_NS") or "surriti_go",
        database=database,
        username=os.environ.get("SURRITI_SURREAL_USER") or "root",
        password=os.environ["SURRITI_SURREAL_PASS"],
    )
    s = Surriti(driver=driver, cognition=False, profile_refresh="off", alias_resolution=False)
    await s.connect()
    try:
        if action == "seed":
            first = await s.add_triplet(subject_name="Straße", predicate="works_at", object_name="Acme", group_id="bridge", valid_at=datetime(2020, 1, 2, 3, 4, 5, 123456, tzinfo=timezone.utc))
            Path(path).write_text(json.dumps({"subject": first.nodes[0].uuid, "first_edge": first.edges[0].uuid}))
        elif action == "verify_export":
            subject = (await driver.query("SELECT uuid FROM entity WHERE group_id='bridge' AND name='Straße';"))[0]["uuid"]
            current = await s.get_current_fact(subject_uuid=subject, predicate="works_at", group_id="bridge")
            assert current is not None and "Beta" in current.fact
            old = await s.get_facts_as_of(subject_uuid=subject, as_of=datetime(2021, 1, 1, tzinfo=timezone.utc), group_id="bridge")
            assert len(old) == 1 and "Acme" in old[0].fact
            assert old[0].valid_at.microsecond == 123456
            await export_group_to_zip(driver, "bridge", path, include_embeddings="always")
        elif action == "verify_import":
            assert validate_pack_zip(path).ok
            result = await import_group_from_zip(driver, path, "python_import")
            assert result.validation.ok and result.counts["edges"] == 2
            rows = await driver.query("SELECT * FROM relates_to WHERE group_id='python_import';")
            assert len(rows) == 2
            assert sum(row["status"] == "active" for row in rows) == 1
        else:
            raise ValueError(action)
    finally:
        await s.close()

asyncio.run(main())
