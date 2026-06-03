.PHONY: build build-server build-worker build-all docker-build docker-push helm-template helm-install helm-upgrade helm-status helm-delete dev compose-up compose-down compose-logs

HELM ?= helm
HELM_DIR = helm/judgex

# --- Local build ---
build: build-server

build-server:
	go build -ldflags="-s -w" -o server ./cmd/server

build-worker:
	go build -ldflags="-s -w" -o judge-worker ./cmd/judge-worker

build-all: build-server build-worker

# --- Docker ---
DOCKER_REGISTRY ?= localhost
BACKEND_TAG ?= latest
FRONTEND_TAG ?= latest

docker-build:
	docker build -t $(DOCKER_REGISTRY)/judgex-backend:$(BACKEND_TAG) .
	docker build -t $(DOCKER_REGISTRY)/judgex-frontend:$(FRONTEND_TAG) ./frontend

docker-push:
	docker push $(DOCKER_REGISTRY)/judgex-backend:$(BACKEND_TAG)
	docker push $(DOCKER_REGISTRY)/judgex-frontend:$(FRONTEND_TAG)

# --- Helm (K8s) ---
HELM_VALUES ?= $(HELM_DIR)/values-prod.yaml

helm-template:
	$(HELM) template judgex $(HELM_DIR)

helm-install: helm-template
	$(HELM) upgrade --install judgex $(HELM_DIR) --values $(HELM_VALUES)

helm-upgrade: helm-install

helm-status:
	kubectl -n judgex get all,pvc,ingress,scaledobject,hpa

helm-delete:
	$(HELM) uninstall judgex -n judgex

# --- Legacy kubectl (deprecated in favor of Helm) ---
K8S_DIR = k8s

k8s-apply:
	kubectl apply -f $(K8S_DIR)/00-namespace.yaml
	kubectl apply -f $(K8S_DIR)/01-configmap.yaml
	kubectl apply -f $(K8S_DIR)/01-secret.yaml
	kubectl apply -f $(K8S_DIR)/06-runtimeclass.yaml
	kubectl apply -f $(K8S_DIR)/10-mysql.yaml
	kubectl apply -f $(K8S_DIR)/11-redis.yaml
	kubectl apply -f $(K8S_DIR)/12-nsq.yaml
	kubectl apply -f $(K8S_DIR)/50-pvc.yaml
	kubectl apply -f $(K8S_DIR)/20-backend.yaml
	kubectl apply -f $(K8S_DIR)/21-backend-hpa.yaml
	kubectl apply -f $(K8S_DIR)/22-judge-worker.yaml
	kubectl apply -f $(K8S_DIR)/23-judge-worker-scaledobject.yaml
	kubectl apply -f $(K8S_DIR)/31-frontend-configmap.yaml
	kubectl apply -f $(K8S_DIR)/30-frontend.yaml
	kubectl apply -f $(K8S_DIR)/40-ingress.yaml

k8s-delete:
	kubectl delete namespace judgex

k8s-status:
	kubectl -n judgex get all,pvc,ingress,scaledobject,hpa

# --- Docker Compose (local dev) ---
compose-up:
	docker compose up -d --build

compose-down:
	docker compose down

compose-logs:
	docker compose logs -f

# --- Development ---
dev:
	go run ./cmd/server
