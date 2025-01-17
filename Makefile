
BUILD_IMAGE  = golang:1.19
PROJECT_NAME = mosn.io/envoy-go-extension

COMPILE_MODE = dbg
TARGET ?= "//:envoy"

TEST_COMPILE_MODE = fastbuild
TEST_TARGET ?= "//test/..."
TEST_LOG_LEVEL = debug
# more custom options
BUILD_OPTS ?=

IMAGE_NAME = "envoy-go-extension"
IMAGE_TAG = "latest"

# go so
.PHONY: build-so-local, build-so

build-so-local:
	go build \
		-v \
		--buildmode=c-shared \
		-o libgolang.so \
		.

build-so:
	docker run --rm -v $(shell pwd):/go/src/${PROJECT_NAME} -w /go/src/${PROJECT_NAME} ${BUILD_IMAGE} make build-so-local

.PHONY: check-test-data-compile-local, check-test-data-compile
check-test-data-compile-local:
	./scripts/check-test-data-compile.sh

check-test-data-compile:
	docker run --rm -v $(shell pwd):/go/src/${PROJECT_NAME} -w /go/src/${PROJECT_NAME} ${BUILD_IMAGE} make check-test-data-compile-local

.PHONY: sync-headers, sync-headers-local
sync-headers:
	docker run --rm -v $(shell pwd):/go/src/${PROJECT_NAME} -w /go/src/${PROJECT_NAME} ${BUILD_IMAGE} make sync-headers-local

sync-headers-local: build-so-local
	cp libgolang.h pkg/api/api.h src/envoy/common/dso/
	rm libgolang.h libgolang.so

# envoy extension
.PHONY: build-envoy test-envoy

build-envoy:
	bazel build \
		-c ${COMPILE_MODE} \
		${TARGET} \
			--verbose_failures \
			${BUILD_OPTS}

test-envoy:
	# remove test_data vendor
	find test/http/golang/test_data/ -name "vendor" | xargs rm -rf
	bazel test \
		-c ${TEST_COMPILE_MODE} \
		${TEST_TARGET} \
			--test_arg="-l ${TEST_LOG_LEVEL}" \
			--test_env=ENVOY_IP_TEST_VERSIONS=v4only \
			--test_env=GODEBUG=cgocheck=0 \
			--test_verbose_timeout_warnings \
			--verbose_failures \
			${BUILD_OPTS}


.PHONY: image
image: build-envoy build-so-local
	# bazel-bin is a soft link
	cp -f bazel-bin/envoy envoy
	sudo docker build --no-cache -t ${IMAGE_NAME}:${IMAGE_TAG} .
	sudo docker tag ${IMAGE_NAME}:${IMAGE_TAG} mosnio/${IMAGE_NAME}:${IMAGE_TAG}
	sudo docker push mosnio/${IMAGE_NAME}:${IMAGE_TAG}


.PHONY: run
run:
	GODEBUG=cgocheck=0 \
	./bazel-bin/envoy \
		-c envoy-golang.yaml \
		-l debug \
		--concurrency 4 \
		--base-id 1

.PHONY: gen-toc
gen-toc:
	# gh-md-toc from https://github.com/ekalinin/github-markdown-toc
	gh-md-toc --insert --no-backup --hide-footer README.md
	sed -i '/#table-of-contents/d' README.md

# update go_repository in WORKSPACE
.PHONY: update-repos
update-repos:
	gazelle update-repos -from_file=go.mod

# still need to update pkg/http/BUILD manually.
.PHONY: gen-build
gen-build:
	gazelle \
		-build_file_name BUILD \
		-exclude api \
		-exclude src \
		-exclude test \
		-exclude samples \
		-external external \
		-repo_root=. \
		-go_prefix=mosn.io/envoy-go-extension

.PHONY: clang-format
clang-format:
	find . -name "*.cc" | xargs clang-format -i
	find . -name "*.h" | grep -v libgolang.h | xargs clang-format -i
