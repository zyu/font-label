SHELL := /bin/bash
DATABASE_URL ?= postgres://postgres:fontlabel@127.0.0.1:15433/fontlabel_dev?sslmode=disable
STORAGE_ROOT ?= $(CURDIR)/storage
TEST_DATABASE_URL ?= $(DATABASE_URL)
# 固定开发用密钥：否则每次 make api 重启都会换秘密，已登录的 token 和图片签名全部失效
JWT_SECRET ?= dev-jwt-secret-font-label-0123456789abcdef
MEDIA_SIGN_SECRET ?= dev-media-secret-font-label-0123456789

.PHONY: help api web test test-concurrency e2e build docker-up docker-down docker-logs clean-exports

help:
	@echo "make api                 启动后端 :18090"
	@echo "make web                 启动前端 :5180"
	@echo "make test                go vet + go test（含并发领取测试，需 TEST_DATABASE_URL）"
	@echo "make e2e                 端到端回归断言（需要后端已启动）"
	@echo "make build               后端二进制 + 前端产物"
	@echo "make docker-up           生产部署（deploy/docker-compose）"
	@echo "make clean-exports       清掉 24h 前的导出临时文件"

api:
	cd server && DATABASE_URL='$(DATABASE_URL)' STORAGE_ROOT='$(STORAGE_ROOT)' \
	  JWT_SECRET='$(JWT_SECRET)' MEDIA_SIGN_SECRET='$(MEDIA_SIGN_SECRET)' go run ./cmd/api

web:
	cd web && npm run dev

test:
	cd server && go vet ./... && TEST_DATABASE_URL='$(TEST_DATABASE_URL)' go test ./...

test-concurrency:
	cd server && TEST_DATABASE_URL='$(TEST_DATABASE_URL)' go test ./internal/app -run ConcurrentClaim -v

e2e:
	bash server/scripts/verify_e2e.sh

build:
	cd server && go build -trimpath -o bin/api ./cmd/api
	cd web && npm run build

docker-up:
	cd deploy && docker compose up -d --build

docker-down:
	cd deploy && docker compose down

docker-logs:
	cd deploy && docker compose logs -f --tail=100

clean-exports:
	find storage/.exports -type f -mtime +1 -delete 2>/dev/null || true
