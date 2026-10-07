#!/bin/bash
# Measure the impact of BUILD-CONTAINER: veth events, container count, and build time.
#
# Usage: ./measure-build-container.sh <before|after|both>
# Output: Measurements of kernel events, container count, and build timing.
#
# Note: Requires root or appropriate capabilities to read udev events via netlink.

set -Eeuo pipefail

mode="${1:-both}"
measurement_duration=30  # seconds

die() {
	printf 'measure: %s\n' "$*" >&2
	exit 1
}

measure_veth_events() {
	local duration=$1
	# Count veth create/destroy events over the measurement period
	# Uses ip monitor to track link events (veth interfaces)
	timeout "$duration" ip monitor link 2>/dev/null | grep -c 'veth' || echo 0
}

measure_container_count() {
	# Count total number of containers created during the build
	docker ps -a --no-trunc --quiet | wc -l
}

measure_build_time() {
	local runs=$1
	# Run go build N times and measure total time
	local start_time=$(date +%s%3N)
	for ((i = 1; i <= runs; i++)); do
		timeout 120 go build ./... >/dev/null 2>&1 || true
	done
	local end_time=$(date +%s%3N)
	echo $((end_time - start_time))
}

measure_without_container() {
	echo "=== BEFORE: Per-command docker run --rm ==="

	# Clear environment to force per-command containers
	unset CANDACE_BUILD_CONTAINER_ID 2>/dev/null || true

	echo "Starting measurement (${measurement_duration}s)..."
	veth_events=$(measure_veth_events $measurement_duration)
	container_count=$(measure_container_count)

	echo "Building 3 times..."
	build_time=$(measure_build_time 3)

	echo "Veth events created: ${veth_events}"
	echo "Total containers: ${container_count}"
	echo "Build time (3 runs, ms): ${build_time}"
	echo
}

measure_with_container() {
	echo "=== AFTER: Long-lived build container ==="

	# Create a long-lived build container
	bazel_image=$(cat bazel/execution_image.txt | tr -d '\n')
	cache_root="${TMPDIR:-/tmp}/candace-bazel-cache"
	mkdir -p "$cache_root/home" "$cache_root/output"

	container_id=$(docker run --detach \
		--network none \
		--user "$(id -u):$(id -g)" \
		--env HOME=/bazel-home \
		--env USER=bazel \
		--volume "$cache_root/home:/bazel-home" \
		--volume "$cache_root/output:/bazel-output" \
		--volume "$(pwd):$(pwd)" \
		--workdir "$(pwd)" \
		--entrypoint sleep \
		"$bazel_image" infinity)

	echo "Created container: ${container_id:0:12}"

	export CANDACE_BUILD_CONTAINER_ID="$container_id"

	echo "Starting measurement (${measurement_duration}s)..."
	veth_events=$(measure_veth_events $measurement_duration)
	container_count_before=$(measure_container_count)

	echo "Building 3 times with container..."
	build_time=$(measure_build_time 3)

	container_count_after=$(measure_container_count)

	echo "Veth events created: ${veth_events}"
	echo "Containers before builds: ${container_count_before}"
	echo "Containers after builds: ${container_count_after}"
	echo "New containers created: $((container_count_after - container_count_before))"
	echo "Build time (3 runs, ms): ${build_time}"

	docker rm -f "$container_id" >/dev/null 2>&1 || true
	echo
}

case "$mode" in
	before)
		measure_without_container
		;;
	after)
		measure_with_container
		;;
	both)
		measure_without_container
		measure_with_container
		;;
	*)
		die "Invalid mode: $mode (use before, after, or both)"
		;;
esac
