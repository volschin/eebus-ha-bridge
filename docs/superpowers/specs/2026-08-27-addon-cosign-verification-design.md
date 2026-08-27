# Verified Home Assistant Add-on Source Image Design

**Date:** 2026-08-27

## Goal

The Home Assistant Supervisor must only package `/usr/local/bin/eebus-bridge` from a bridge image whose immutable multi-platform manifest digest was signed by this repository's release workflow. Verification must happen while Supervisor builds the add-on, fail closed, and add no registry or Sigstore dependency to normal add-on startup.

## Context

The add-on currently uses `ghcr.io/volschin/eebus-bridge:${BUILD_VERSION}` as a Dockerfile stage and copies one static binary from it. `BUILD_VERSION` comes from `config.yaml`; Supervisor builds the add-on locally on the user's machine. The release workflow now signs the pushed multi-platform digest keylessly with Cosign and verifies the signature before publishing the GitHub release.

A digest cannot be committed alongside the version that creates it: the digest is only known after the tagged commit has built and pushed the image. Runtime verification in `run.sh` would happen after the image was built, would not prove which digest supplied the copied binary unless that digest were already embedded, and would make every start depend on registry availability.

## Non-goals

- Do not add Cosign or registry tools to the final add-on image.
- Do not verify signatures during each add-on start.
- Do not replace or digest-pin the existing Home Assistant base image in this change.
- Do not protect against a compromised authorized release workflow. A correctly signed malicious release remains valid under this model.
- Do not add fallback behavior for unsigned bridge images.
- Do not change EEBUS runtime behavior, configuration, networking, or persistent data.

## Architecture

### Tool stages

Use official multi-platform tool images pinned by tag and index digest:

- `ghcr.io/sigstore/cosign/cosign:v3.1.3@sha256:9e5c2f2edc34351160407ca3416c61855bdf9403c3c5936e0f0be7fc261611b8`
  - binary: `/ko-app/cosign`
  - supports `linux/amd64`, `linux/arm64`, and `linux/arm/v7`
- `ghcr.io/regclient/regctl:v0.11.5@sha256:dbe356c6cf9f8f85e302b9e47fed481ef3f1b04807350e99b02ab2cadee0a993`
  - binary: `/regctl`
  - supports `linux/amd64`, `linux/arm64`, and `linux/arm/v7`

The tag documents the tool version. The digest determines immutable content and lets Renovate propose reviewed digest updates. Crane is not used because its official v0.22.0 image lacks `linux/arm/v7`.

### Verification stage

The Dockerfile no longer declares the bridge image as `FROM`. A `verified-bridge` stage based on the existing Home Assistant base image receives the two static tool binaries and the verification script.

The script interface is:

```text
fetch-verified-bridge.sh <version> <output-path>
```

For version `X.Y.Z`, it performs these operations in order:

1. Validate that the version exactly matches `^[0-9]+\.[0-9]+\.[0-9]+$`.
2. Resolve `ghcr.io/volschin/eebus-bridge:X.Y.Z` to its multi-platform index digest with `regctl image digest` and no platform selection.
3. Validate that the result exactly matches `sha256:` followed by 64 lowercase hexadecimal characters.
4. Construct `ghcr.io/volschin/eebus-bridge@sha256:...`.
5. Verify that digest with Cosign using:
   - certificate identity `https://github.com/volschin/eebus-ha-bridge/.github/workflows/release.yml@refs/tags/vX.Y.Z`
   - OIDC issuer `https://token.actions.githubusercontent.com`
6. Only after successful verification, extract `/usr/local/bin/eebus-bridge` from the same digest with `regctl image get-file --platform local`.
7. Require a non-empty executable output file.

The final add-on stage copies only that output file, `run.sh`, and existing runtime content. Cosign, regctl, and the verification script remain in intermediate stages.

### Data flow

```text
BUILD_VERSION
    |
    v
mutable version tag --resolve once--> immutable index digest
                                         |
                                         +--> exact Cosign identity verification
                                         |
                                         +--> platform-local binary extraction
                                                   |
                                                   v
                                         final Home Assistant add-on image
```

The signed index commits to every referenced platform manifest. `--platform local` chooses the native Supervisor platform from that signed index.

## Security properties

- The version tag is only used to discover one digest. All security-sensitive operations after discovery use the digest.
- A tag change after discovery cannot alter either the verified subject or downloaded binary.
- Verification uses an exact certificate identity and issuer, not regular expressions.
- No transparency-log or certificate checks are disabled.
- Registry, DNS, Sigstore, malformed version, malformed digest, missing signature, wrong identity, extraction, and output validation failures all terminate the build.
- No unsigned fallback exists.
- A failed install or update cannot produce a new add-on image. An already-built add-on has no Cosign, registry, or network dependency when it starts.
- Tool images are immutable digest-pinned bootstrap dependencies. Updating either digest requires the normal dependency PR and CI path.
- BuildKit may reuse a previously successful verification layer for an unchanged version and unchanged tools. Such a cache hit can only preserve the old verified binary; it cannot substitute content from a subsequently moved tag.

## Automated tests

### Verification script tests

Add focused tests that place fake `cosign` and `regctl` executables first on `PATH` while exercising the real shell script. They must prove:

- malformed versions fail before any registry command;
- digest-resolution failure propagates;
- malformed digest output fails before Cosign;
- Cosign failure prevents `image get-file`;
- success passes the exact same digest reference to Cosign and `image get-file`;
- success passes the exact workflow identity and GitHub OIDC issuer;
- success creates a non-empty executable output;
- extraction failure or empty output fails closed.

Mocks are limited to unavoidable external registry CLIs. Assertions target command order, arguments, output, and exit status of the production script.

### Static Dockerfile tests

Assert that:

- the bridge repository is no longer used by a `FROM` instruction;
- Cosign and regctl references include the approved full index digests;
- only the verified output is copied into the final image;
- the final stage does not contain Cosign, regctl, or the verification script.

### Pull-request CI

The existing add-on job continues to run Hadolint, the Supervisor add-on linter, and version synchronization. It additionally:

1. runs verification-script and Dockerfile contract tests;
2. confirms the pinned Cosign and regctl indexes contain `linux/amd64`, `linux/arm64`, and `linux/arm/v7`;
3. builds the add-on on `linux/amd64` against the current already-signed release version.

### Release CI

Extend release ordering to:

```text
publish-image -> sign-image -> build-verified-addon -> create-release
```

`build-verified-addon` checks out the tagged source and builds the add-on on `linux/amd64` with `BUILD_VERSION` derived from the tag. It has only `contents: read`; the source image and signature are public. A failed verification or extraction prevents GitHub Release publication.

The release build does not emulate other architectures. Platform availability is checked from the pinned tool indexes in PR CI, while users build natively on their Supervisor hardware.

## Supervisor acceptance test

Use Home Assistant's official app development devcontainer, which starts a real Supervisor and Home Assistant instance and maps the repository as a local app.

After the bootstrap release and implementation:

1. Start Home Assistant through the devcontainer task.
2. Install the local EEBUS Bridge app through Supervisor.
3. Confirm Supervisor build logs show digest resolution, successful identity verification, and extraction.
4. Start the app and confirm the bridge reaches its normal running state.
5. Stop and restart the app; confirm startup performs no Cosign or registry access.
6. Force a Supervisor rebuild and confirm success.
7. Temporarily alter the expected certificate identity in the local test checkout.
8. Force another rebuild and confirm Supervisor reports build failure and no unverified image is produced.
9. Restore the identity and confirm a subsequent forced rebuild and start succeed.

Run the same install/build/start flow once on a real Home Assistant OS or Supervised system when available. Native ARM/v7 execution cannot be fully proven by an amd64 hosted runner without QEMU; the pinned manifest check preserves coverage until hardware validation is possible.

## Rollout

The currently referenced v0.16.5 bridge image predates Cosign signing. Enabling fail-closed verification against it would break new local builds.

Rollout therefore uses separate release operations:

1. Create a version-only v0.16.6 release from current `main` before merging implementation. The already-merged release workflow builds and signs its bridge image.
2. Verify the v0.16.6 image signature explicitly against the exact workflow identity and issuer.
3. Rebase the feature branch onto the v0.16.6 version commit.
4. Implement and test the verifier against signed v0.16.6.
5. Merge the feature PR after automated and Supervisor acceptance gates pass.
6. Create a separate v0.16.7 release so existing add-on installations receive an update that contains the verified build path.
7. Confirm v0.16.7 passes `build-verified-addon`, publishes its GitHub release, and verifies with Cosign.

Version changes and release tags remain separate from the feature commit, preserving repository release policy.

There is an unavoidable short interval after a future version commit reaches `main` and before its tag workflow has signed the image. A Supervisor build attempted in that interval fails closed and can be retried after the release completes.

## Acceptance criteria

- No `FROM ghcr.io/volschin/eebus-bridge:*` remains in the add-on Dockerfile.
- Tag resolution occurs once; verification and extraction use the same validated digest.
- Only the expected release workflow identity and GitHub OIDC issuer are accepted.
- Cosign/regctl support all three add-on architectures and are digest-pinned.
- Verification tools do not exist in the final add-on image.
- Unit, static, amd64 integration, release-gate, and Supervisor devcontainer tests pass.
- Negative identity testing proves Supervisor rebuild fails closed.
- v0.16.6 is signed before implementation is merged.
- v0.16.7 delivers the verifier to existing installations after feature merge.

## References

- [Sigstore image verification](https://docs.sigstore.dev/cosign/verifying/verify/)
- [Sigstore Cosign installation and image identity](https://docs.sigstore.dev/cosign/system_config/installation/)
- [regctl image get-file](https://regclient.org/cli/regctl/image/get-file/)
- [regctl image export](https://regclient.org/cli/regctl/image/export/)
- [Home Assistant local app testing](https://developers.home-assistant.io/docs/apps/testing/)
- [Home Assistant app configuration](https://developers.home-assistant.io/docs/apps/configuration/)
- [Home Assistant app publishing](https://developers.home-assistant.io/docs/add-ons/publishing/)
