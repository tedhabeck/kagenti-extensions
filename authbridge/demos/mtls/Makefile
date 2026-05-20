.DEFAULT_GOAL := help

NAMESPACE ?= team1
CALLER    ?= mtls-caller
CALLEE    ?= mtls-callee

.PHONY: help
help:
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  \033[36m%-26s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

.PHONY: deploy
deploy: ## Deploy caller + callee pods (both with mtls.mode: strict)
	kubectl apply -f k8s/caller.yaml
	kubectl apply -f k8s/callee.yaml
	./scripts/patch-mtls-config.sh $(NAMESPACE) $(CALLER) strict
	./scripts/patch-mtls-config.sh $(NAMESPACE) $(CALLEE) strict
	kubectl -n $(NAMESPACE) rollout restart deploy/$(CALLER) deploy/$(CALLEE)
	kubectl -n $(NAMESPACE) rollout status deploy/$(CALLER) deploy/$(CALLEE) --timeout=120s

.PHONY: demo-mtls
demo-mtls: deploy ## Deploy + run the encryption-on-the-wire check (strict mode)
	./scripts/verify-encrypted.sh $(NAMESPACE) $(CALLER) $(CALLEE)

.PHONY: demo-mtls-permissive
demo-mtls-permissive: ## Same shape but with mtls.mode: permissive; verify mixed-mode serving
	kubectl apply -f k8s/caller.yaml
	kubectl apply -f k8s/callee.yaml
	./scripts/patch-mtls-config.sh $(NAMESPACE) $(CALLER) permissive
	./scripts/patch-mtls-config.sh $(NAMESPACE) $(CALLEE) permissive
	kubectl -n $(NAMESPACE) rollout restart deploy/$(CALLER) deploy/$(CALLEE)
	kubectl -n $(NAMESPACE) rollout status deploy/$(CALLER) deploy/$(CALLEE) --timeout=120s
	./scripts/verify-permissive.sh $(NAMESPACE) $(CALLEE)

.PHONY: demo-mtls-strict-rejects-plain
demo-mtls-strict-rejects-plain: deploy ## Verify that plain HTTP gets dropped on a strict callee
	./scripts/verify-strict-rejects-plain.sh $(NAMESPACE) $(CALLEE)

.PHONY: undeploy
undeploy: ## Tear down the demo's resources
	kubectl delete -f k8s/caller.yaml --ignore-not-found
	kubectl delete -f k8s/callee.yaml --ignore-not-found
