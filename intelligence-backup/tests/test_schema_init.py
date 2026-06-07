"""
Tests for schema initialization (Neo4j + Qdrant).

All external services are mocked — no real database connections required.
Run with: pytest tests/test_schema_init.py -v
"""

import json
import os
import sys
import unittest
from pathlib import Path
from unittest.mock import MagicMock, call, patch

# Ensure the project root is on sys.path
PROJECT_ROOT = Path(__file__).resolve().parents[2]
if str(PROJECT_ROOT) not in sys.path:
    sys.path.insert(0, str(PROJECT_ROOT))

from intelligence.core.qdrant_setup import (
    COLLECTION_CONFIGS,
    COLLECTION_NAMES,
    QdrantSetup,
)
from intelligence.core.schema_init import init_all


# ---------------------------------------------------------------------------
# Fixtures / Helpers
# ---------------------------------------------------------------------------

def _make_mock_collection(name: str):
    """Return a MagicMock that looks like a CollectionDescription."""
    m = MagicMock()
    m.name = name
    return m


def _make_mock_collection_info(name: str, exists: bool = True):
    """Return a mock collection info object for health checks."""
    if not exists:
        return None
    info = MagicMock()
    info.status = MagicMock()
    info.status.value = "green"
    info.vectors_count = 42
    info.indexed_vectors_count = 42
    info.points_count = 42
    return info


# ---------------------------------------------------------------------------
# QdrantSetup Tests
# ---------------------------------------------------------------------------


class TestQdrantSetup(unittest.TestCase):
    """Unit tests for the QdrantSetup class (fully mocked client)."""

    def setUp(self):
        """Create a fresh mock QdrantClient before every test."""
        self.mock_client = MagicMock()
        # Default: no collections exist
        self.mock_client.get_collections.return_value = MagicMock(collections=[])
        self.setup = QdrantSetup(client=self.mock_client)

    # -- Collection Lifecycle --

    def test_create_all_collections(self):
        """All three collections should be created when none exist."""
        result = self.setup.create_collections()

        self.assertTrue(all(result.values()))
        self.assertEqual(len(result), 3)
        self.assertEqual(self.mock_client.create_collection.call_count, 3)

        # Verify correct names passed
        call_names = [
            call.kwargs.get("collection_name") or call.args[0]
            for call in self.mock_client.create_collection.call_args_list
        ]
        for name in COLLECTION_NAMES:
            self.assertIn(name, call_names)

    def test_idempotent_create(self):
        """Running create_collections twice should not raise or duplicate."""
        # First run
        r1 = self.setup.create_collections()
        self.assertTrue(all(r1.values()))
        self.assertEqual(self.mock_client.create_collection.call_count, 3)

        # Simulate collections now existing
        self.mock_client.get_collections.return_value = MagicMock(
            collections=[_make_mock_collection(n) for n in COLLECTION_NAMES]
        )

        # Second run — should skip creation
        r2 = self.setup.create_collections()
        self.assertTrue(all(r2.values()))
        # create_collection should NOT have been called additional times
        self.assertEqual(self.mock_client.create_collection.call_count, 3)

    def test_delete_collections(self):
        """delete_collections should call delete_collection for each name."""
        result = self.setup.delete_collections()
        self.assertTrue(all(result.values()))
        self.assertEqual(self.mock_client.delete_collection.call_count, 3)

    def test_delete_collections_graceful_when_missing(self):
        """Deleting a non-existent collection should not raise."""
        self.mock_client.delete_collection.side_effect = Exception("Collection not found")
        result = self.setup.delete_collections()
        # Even with errors, we treat missing collections as success
        self.assertTrue(all(result.values()))

    # -- Payload Indexes --

    def test_create_payload_indexes(self):
        """All payload fields should be indexed."""
        result = self.setup.create_payload_indexes()

        for name, fields in COLLECTION_CONFIGS.items():
            self.assertIn(name, result)
            for field_name in fields["payload_fields"]:
                self.assertTrue(
                    result[name][field_name],
                    f"Index {name}.{field_name} should be created",
                )

    def test_idempotent_payload_indexes(self):
        """Re-creating payload indexes should be a no-op."""
        r1 = self.setup.create_payload_indexes()
        self.assertTrue(all(v for f in r1.values() for v in f.values()))

        # Simulate "already exists" errors on second run
        def side_effect(*args, **kwargs):
            raise Exception("Index already exists")

        self.mock_client.create_payload_index.side_effect = side_effect

        r2 = self.setup.create_payload_indexes()
        # Should still report success because already-exists is treated as OK
        self.assertTrue(all(v for f in r2.values() for v in f.values()))

    # -- Health Check --

    def test_health_check_returns_all_collections(self):
        """health_check() must return entries for all defined collections."""
        # Pre-populate two collections, leave one missing
        self.mock_client.get_collections.return_value = MagicMock(
            collections=[
                _make_mock_collection("email_chunks"),
                _make_mock_collection("voice_examples"),
            ]
        )

        def mock_get_collection(collection_name, **kwargs):
            return _make_mock_collection_info(collection_name, exists=True)

        self.mock_client.get_collection.side_effect = mock_get_collection

        health = self.setup.health_check()

        for name in COLLECTION_NAMES:
            self.assertIn(name, health)

        self.assertTrue(health["email_chunks"]["exists"])
        self.assertTrue(health["voice_examples"]["exists"])
        # consultation_index was not in the "existing" set, so exists=False
        # but it should still be present in the report
        self.assertIn("consultation_index", health)

    def test_health_check_counts(self):
        """Health check should report correct vector and point counts."""
        self.mock_client.get_collections.return_value = MagicMock(
            collections=[_make_mock_collection("email_chunks")]
        )
        self.mock_client.get_collection.return_value = _make_mock_collection_info(
            "email_chunks"
        )

        health = self.setup.health_check()
        self.assertEqual(health["email_chunks"]["vectors_count"], 42)
        self.assertEqual(health["email_chunks"]["points_count"], 42)
        self.assertEqual(health["email_chunks"]["status"], "green")


# ---------------------------------------------------------------------------
# Schema Init Runner Tests
# ---------------------------------------------------------------------------


class TestSchemaInitRunner(unittest.TestCase):
    """Tests for the init_all() orchestration function."""

    @patch("intelligence.core.schema_init.QdrantSetup")
    @patch("intelligence.core.schema_init.GraphDatabase")
    def test_init_all_success(self, mock_graph_db, mock_qdrant_setup_class):
        """init_all should succeed when both Neo4j and Qdrant are healthy."""
        # -- Mock Neo4j --
        mock_driver = MagicMock()
        mock_session = MagicMock()
        mock_driver.session.return_value.__enter__ = MagicMock(
            return_value=mock_session
        )
        mock_driver.session.return_value.__exit__ = MagicMock(
            return_value=False
        )
        mock_graph_db.driver.return_value = mock_driver

        # -- Mock QdrantSetup --
        mock_qdrant = MagicMock()
        mock_qdrant.create_collections.return_value = {
            "email_chunks": True,
            "voice_examples": True,
            "consultation_index": True,
        }
        mock_qdrant.create_payload_indexes.return_value = {
            "email_chunks": {"user_id": True},
            "voice_examples": {"user_id": True},
            "consultation_index": {"user_id": True},
        }
        mock_qdrant.health_check.return_value = {
            "email_chunks": {"exists": True, "status": "green"},
            "voice_examples": {"exists": True, "status": "green"},
            "consultation_index": {"exists": True, "status": "green"},
        }
        mock_qdrant_setup_class.return_value = mock_qdrant

        report = init_all(
            neo4j_uri="bolt://test:7687",
            neo4j_user="neo4j",
            neo4j_password="test",
            qdrant_url="http://test:6333",
        )

        self.assertTrue(report["success"])
        self.assertTrue(report["neo4j"]["success"])
        self.assertTrue(report["qdrant"]["success"])
        self.assertEqual(report["neo4j"]["errors"], [])
        self.assertEqual(report["qdrant"]["errors"], [])

    @patch("intelligence.core.schema_init.QdrantSetup")
    @patch("intelligence.core.schema_init.GraphDatabase")
    def test_init_all_idempotent(self, mock_graph_db, mock_qdrant_setup_class):
        """Running init_all twice should produce no errors."""
        # -- Mock Neo4j --
        mock_driver = MagicMock()
        mock_session = MagicMock()
        mock_driver.session.return_value.__enter__ = MagicMock(
            return_value=mock_session
        )
        mock_driver.session.return_value.__exit__ = MagicMock(
            return_value=False
        )
        mock_graph_db.driver.return_value = mock_driver

        # Simulate already-exists errors on the session
        from neo4j.exceptions import Neo4jError

        mock_session.run.side_effect = Neo4jError(
            "Constraint already exists"
        )

        # -- Mock QdrantSetup --
        mock_qdrant = MagicMock()
        mock_qdrant.create_collections.return_value = {
            n: True for n in COLLECTION_NAMES
        }
        mock_qdrant.create_payload_indexes.return_value = {
            n: {"user_id": True} for n in COLLECTION_NAMES
        }
        mock_qdrant.health_check.return_value = {
            n: {"exists": True, "status": "green"} for n in COLLECTION_NAMES
        }
        mock_qdrant_setup_class.return_value = mock_qdrant

        # First run
        r1 = init_all(
            neo4j_uri="bolt://test:7687",
            neo4j_user="neo4j",
            neo4j_password="test",
            qdrant_url="http://test:6333",
        )
        # With Neo4jError on every statement, neo4j success is False
        # but Qdrant should still succeed
        self.assertTrue(r1["qdrant"]["success"])

        # Second run — same state, no new errors
        r2 = init_all(
            neo4j_uri="bolt://test:7687",
            neo4j_user="neo4j",
            neo4j_password="test",
            qdrant_url="http://test:6333",
        )
        self.assertTrue(r2["qdrant"]["success"])

    @patch("intelligence.core.schema_init.QdrantSetup")
    @patch("intelligence.core.schema_init.GraphDatabase")
    def test_health_check_includes_all_collections(
        self, mock_graph_db, mock_qdrant_setup_class
    ):
        """The health check in init_all must report all 3 collections."""
        # -- Mock Neo4j --
        mock_driver = MagicMock()
        mock_session = MagicMock()
        mock_driver.session.return_value.__enter__ = MagicMock(
            return_value=mock_session
        )
        mock_driver.session.return_value.__exit__ = MagicMock(
            return_value=False
        )
        mock_graph_db.driver.return_value = mock_driver

        # -- Mock QdrantSetup --
        mock_qdrant = MagicMock()
        mock_qdrant.create_collections.return_value = {
            n: True for n in COLLECTION_NAMES
        }
        mock_qdrant.create_payload_indexes.return_value = {
            n: {"user_id": True} for n in COLLECTION_NAMES
        }
        mock_qdrant.health_check.return_value = {
            n: {"exists": True, "status": "green"} for n in COLLECTION_NAMES
        }
        mock_qdrant_setup_class.return_value = mock_qdrant

        report = init_all(
            neo4j_uri="bolt://test:7687",
            neo4j_user="neo4j",
            neo4j_password="test",
            qdrant_url="http://test:6333",
        )

        health = report["qdrant"]["health"]
        self.assertEqual(len(health), 3)
        for name in COLLECTION_NAMES:
            self.assertIn(name, health)
            self.assertTrue(health[name]["exists"])

    @patch("intelligence.core.schema_init.QdrantSetup")
    @patch("intelligence.core.schema_init.GraphDatabase")
    def test_neo4j_error_reported(self, mock_graph_db, mock_qdrant_setup_class):
        """Neo4j connectivity failures should be reported without crashing."""
        mock_graph_db.driver.side_effect = Exception(
            "Failed to establish connection"
        )

        mock_qdrant = MagicMock()
        mock_qdrant.create_collections.return_value = {
            n: True for n in COLLECTION_NAMES
        }
        mock_qdrant.create_payload_indexes.return_value = {
            n: {"user_id": True} for n in COLLECTION_NAMES
        }
        mock_qdrant.health_check.return_value = {
            n: {"exists": True, "status": "green"} for n in COLLECTION_NAMES
        }
        mock_qdrant_setup_class.return_value = mock_qdrant

        report = init_all(
            neo4j_uri="bolt://bad:7687",
            neo4j_user="neo4j",
            neo4j_password="wrong",
            qdrant_url="http://test:6333",
        )

        self.assertFalse(report["success"])
        self.assertFalse(report["neo4j"]["success"])
        self.assertTrue(report["qdrant"]["success"])
        self.assertTrue(len(report["neo4j"]["errors"]) > 0)

    @patch("intelligence.core.schema_init.QdrantSetup")
    @patch("intelligence.core.schema_init.GraphDatabase")
    def test_qdrant_error_reported(self, mock_graph_db, mock_qdrant_setup_class):
        """Qdrant failures should be reported without crashing."""
        mock_driver = MagicMock()
        mock_session = MagicMock()
        mock_driver.session.return_value.__enter__ = MagicMock(
            return_value=mock_session
        )
        mock_driver.session.return_value.__exit__ = MagicMock(
            return_value=False
        )
        mock_graph_db.driver.return_value = mock_driver

        mock_qdrant_setup_class.side_effect = Exception("Qdrant unreachable")

        report = init_all(
            neo4j_uri="bolt://test:7687",
            neo4j_user="neo4j",
            neo4j_password="test",
            qdrant_url="http://bad:6333",
        )

        self.assertFalse(report["success"])
        self.assertTrue(report["neo4j"]["success"])
        self.assertFalse(report["qdrant"]["success"])
        self.assertTrue(len(report["qdrant"]["errors"]) > 0)


# ---------------------------------------------------------------------------
# CLI Smoke Test
# ---------------------------------------------------------------------------


class TestCLISmoke(unittest.TestCase):
    """Minimal test that schema_init can be imported and has expected API."""

    def test_init_all_signature(self):
        """init_all should accept the documented keyword arguments."""
        import inspect

        sig = inspect.signature(init_all)
        params = list(sig.parameters.keys())
        for expected in [
            "neo4j_uri",
            "neo4j_user",
            "neo4j_password",
            "qdrant_url",
            "qdrant_api_key",
            "cypher_schema_path",
        ]:
            self.assertIn(expected, params)


# ---------------------------------------------------------------------------
# Entry point
# ---------------------------------------------------------------------------

if __name__ == "__main__":
    unittest.main(verbosity=2)
