## Summary
<!-- Brief description of the changes -->

## Test plan
<!-- How were these changes tested? -->

## Checklist
- [ ] **Resolved all CodeRabbit comments**
- [ ] **Tested the PR** (comment `/test ?` for all the tests):
  - [ ] Deployment changes (e.g., bumping a deployment-related variable): `/test deploy`
  - [ ] Upgrade changes: `/test upgrade-management`
  - [ ] Network/TFT changes: `/test network`
  - [ ] Sanity check changes: `/test sanity`
  - [ ] Scale-impacting changes: `/test scale`
  - [ ] Conformance-impacting changes: `/test conformance`
  - [ ] New e2e test: point [`E2E_GO_LABEL_FILTER`](https://github.com/rh-ecosystem-edge/openshift-dpf/blob/main/Makefile#L334) to the new test, run `/test e2e`, then restore the previous value.
- [ ] Updated documentation and/or `AGENTS.md` if conventions changed
- [ ] Ran `make validate-env-files` if env variables were modified
- [ ] New env variables added to `ci/env.defaults`, `ci/env.template`, and `ci/env.required` (if no default)
- [ ] New Makefile targets have a description in `make help`
- [ ] New scripts source `env.sh` and `utils.sh`, use `set -e` and `set -o pipefail`
- [ ] Sensitive values are redacted in logs (no API keys, pull secrets, or credentials in output)
