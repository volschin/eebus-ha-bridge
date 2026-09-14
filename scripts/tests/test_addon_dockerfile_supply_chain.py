from pathlib import Path
import re
import unittest


DOCKERFILE = Path(__file__).parents[2] / "eebus-bridge-addon" / "Dockerfile"
COSIGN_REF = (
    "ghcr.io/sigstore/cosign/cosign:v3.1.3@sha256:"
    "9e5c2f2edc34351160407ca3416c61855bdf9403c3c5936e0f0be7fc261611b8"
)
REGCTL_REF = (
    "ghcr.io/regclient/regctl:v0.11.6@sha256:"
    "db2e5f3a5de80b24d53fea5af9edf7021a53f2689f1e166ef2f0e200dad4fb3a"
)


class AddonDockerfileSupplyChainTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.text = DOCKERFILE.read_text()

    def test_bridge_registry_is_not_a_from_source(self) -> None:
        self.assertNotRegex(
            self.text,
            r"(?m)^FROM\s+ghcr\.io/volschin/eebus-bridge",
        )

    def test_verifier_tools_are_tagged_and_digest_pinned(self) -> None:
        self.assertIn(f"FROM {COSIGN_REF} AS cosign", self.text)
        self.assertIn(f"FROM {REGCTL_REF} AS regctl", self.text)

    def test_final_stage_copies_only_verified_bridge(self) -> None:
        final_stage = self.text.rsplit("FROM ${BUILD_FROM}", 1)[1]
        self.assertIn(
            "COPY --from=verified-bridge /verified/eebus-bridge "
            "/usr/local/bin/eebus-bridge",
            final_stage,
        )
        self.assertNotIn("cosign", final_stage)
        self.assertNotIn("regctl", final_stage)
        self.assertNotIn("fetch-verified-bridge", final_stage)


if __name__ == "__main__":
    unittest.main()
