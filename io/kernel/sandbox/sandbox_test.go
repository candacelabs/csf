// Copyright 2026 Candace Labs

package sandbox

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("DeriveLimits", func() {
	It("caps cpu at eight cores and memory at the smaller of 16 GiB and half the host", func() {
		limits := DeriveLimits(Capacity{Cores: 32, MemoryBytes: 64 << 30})
		Expect(limits.CPUMax).To(Equal("800000 100000"))
		Expect(limits.MemoryMaxBytes).To(Equal(uint64(16 << 30)))
		Expect(limits.MemorySwapMaxBytes).To(Equal(uint64(0)))
		Expect(limits.PidsMax).To(Equal(uint64(2048)))
		Expect(limits.Derivation).To(ContainSubstring("fork bomb"))
	})

	It("uses half the host memory when that is below the cap and never fewer than one core", func() {
		limits := DeriveLimits(Capacity{Cores: 0, MemoryBytes: 8 << 30})
		Expect(limits.CPUMax).To(Equal("100000 100000"))
		Expect(limits.MemoryMaxBytes).To(Equal(uint64(4 << 30)))
	})
})

var _ = Describe("ParseUsage", func() {
	It("reads usage_usec and memory.peak", func() {
		usage, err := ParseUsage([]byte("usage_usec 44757\nuser_usec 10\nsystem_usec 5\n"), []byte("21704704\n"))
		Expect(err).NotTo(HaveOccurred())
		Expect(usage.CPU).To(Equal(44757 * time.Microsecond))
		Expect(usage.MemoryPeakBytes).To(Equal(uint64(21704704)))
	})

	It("fails when cpu.stat has no usage_usec", func() {
		_, err := ParseUsage([]byte("user_usec 10\n"), []byte("1"))
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("the policy JSON", func() {
	It("uses the field names the launcher reads", func() {
		policy := SessionPolicy{
			SessionID: "s1", Cgroup: "/cg", Limits: DeriveLimits(Capacity{Cores: 4, MemoryBytes: 8 << 30}),
			ReadWritePaths: []string{"/w"}, ReadOnlyPaths: []string{"/usr"}, DockerProxySocket: "/sock",
		}
		content, err := marshalPolicy(policy)
		Expect(err).NotTo(HaveOccurred())
		var decoded map[string]json.RawMessage
		Expect(json.Unmarshal(content, &decoded)).To(Succeed())
		Expect(decoded).To(HaveKey("session_id"))
		Expect(decoded).To(HaveKey("cgroup"))
		Expect(decoded).To(HaveKey("read_write_paths"))
		Expect(decoded).To(HaveKey("read_only_paths"))
		Expect(decoded).To(HaveKey("docker_proxy_socket"))
		var limits map[string]json.RawMessage
		Expect(json.Unmarshal(decoded["limits"], &limits)).To(Succeed())
		for _, key := range []string{"cpu_max", "memory_max_bytes", "memory_swap_max_bytes", "pids_max", "derivation"} {
			Expect(limits).To(HaveKey(key))
		}
	})

	It("omits the docker socket when the session has no container access", func() {
		content, err := marshalPolicy(SessionPolicy{SessionID: "s1", Cgroup: "/cg", ReadWritePaths: []string{"/w"}})
		Expect(err).NotTo(HaveOccurred())
		Expect(string(content)).NotTo(ContainSubstring("docker_proxy_socket"))
	})
})

var _ = Describe("delegatedCgroup", func() {
	var root, procSelf string

	BeforeEach(func() {
		root = GinkgoT().TempDir()
		procSelf = filepath.Join(GinkgoT().TempDir(), "cgroup")
	})

	writeControllers := func(suffix string, controllers string) {
		own := filepath.Join(root, suffix)
		Expect(os.MkdirAll(own, 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(own, cgroupControllersFile), []byte(controllers), 0o644)).To(Succeed())
	}

	It("resolves the unified entry when the controllers are delegated", func() {
		Expect(os.WriteFile(procSelf, []byte("0::/harness\n"), 0o644)).To(Succeed())
		writeControllers("harness", "cpu memory pids")
		own, err := delegatedCgroup(procSelf, root)
		Expect(err).NotTo(HaveOccurred())
		Expect(own).To(Equal(filepath.Join(root, "harness")))
	})

	It("reports not delegated when a controller is missing", func() {
		Expect(os.WriteFile(procSelf, []byte("0::/harness\n"), 0o644)).To(Succeed())
		writeControllers("harness", "cpu memory")
		_, err := delegatedCgroup(procSelf, root)
		Expect(err).To(MatchError(ErrNotDelegated))
	})

	It("reports not delegated when there is no unified entry", func() {
		Expect(os.WriteFile(procSelf, []byte("1:name=systemd:/x\n"), 0o644)).To(Succeed())
		_, err := delegatedCgroup(procSelf, root)
		Expect(err).To(MatchError(ErrNotDelegated))
	})
})

var _ = Describe("a Manager over a stand-in cgroup tree", func() {
	var manager *Manager
	var sessionsRoot, policyPath string

	BeforeEach(func() {
		sessionsRoot = GinkgoT().TempDir()
		policyPath = filepath.Join(GinkgoT().TempDir(), "policy.json")
		var err error
		manager, err = NewManager("/opt/csf/launcher",
			WithProxy("/opt/csf/proxy"),
			WithAllowedImages("img@sha256:abc"),
			WithReadOnlyRoots("/usr", "/bin"),
			WithCapacity(Capacity{Cores: 8, MemoryBytes: 32 << 30}))
		Expect(err).NotTo(HaveOccurred())
		manager.sessionsRoot = sessionsRoot
	})

	It("requires the launcher binary", func() {
		_, err := NewManager("")
		Expect(err).To(MatchError(ErrNoLauncher))
	})

	It("refuses to open before Prepare set the sessions cgroup", func() {
		bare, err := NewManager("/l", WithCapacity(Capacity{Cores: 1, MemoryBytes: 1 << 30}))
		Expect(err).NotTo(HaveOccurred())
		_, err = bare.Open(SessionParams{ID: "s1", PolicyPath: policyPath, ReadWrite: []string{"/w"}})
		Expect(err).To(MatchError(ErrNotPrepared))
	})

	It("creates the session cgroup, writes the policy and builds the commands", func() {
		tmp := filepath.Join(GinkgoT().TempDir(), "private-tmp")
		session, err := manager.Open(SessionParams{
			ID: "s1", PolicyPath: policyPath, ReadWrite: []string{"/run/s1/worktree"},
			PrivateTmp: tmp, ProxySocket: "/run/s1/docker.sock",
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(session.Cgroup()).To(Equal(filepath.Join(sessionsRoot, "s1")))
		Expect(session.Cgroup()).To(BeADirectory())
		Expect(tmp).To(BeADirectory())

		var policy SessionPolicy
		content, err := os.ReadFile(policyPath)
		Expect(err).NotTo(HaveOccurred())
		Expect(json.Unmarshal(content, &policy)).To(Succeed())
		Expect(policy.Cgroup).To(Equal(session.Cgroup()))
		Expect(policy.ReadOnlyPaths).To(Equal([]string{"/usr", "/bin"}))
		Expect(policy.ReadWritePaths).To(Equal([]string{"/run/s1/worktree", tmp}))
		Expect(policy.DockerProxySocket).To(Equal("/run/s1/docker.sock"))

		Expect(session.LaunchPrefix()).To(Equal([]string{"/opt/csf/launcher", "--policy", policyPath, "--"}))
		Expect(session.DockerProxyCommand("/var/run/docker.sock")).To(Equal([]string{
			"/opt/csf/proxy", "--socket", "/run/s1/docker.sock", "--upstream", "/var/run/docker.sock",
			"--session", "s1", "--allow-image", "img@sha256:abc", "--session-path", "/run/s1/worktree",
		}))
	})

	It("kills, measures and removes a session cgroup", func() {
		cgroup := filepath.Join(sessionsRoot, "s2")
		Expect(os.Mkdir(cgroup, 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(cgroup, cgroupKillFile), nil, 0o644)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(cgroup, cpuStatFile), []byte("usage_usec 52192\n"), 0o644)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(cgroup, memoryPeakFile), []byte("22315008\n"), 0o644)).To(Succeed())
		session := &Session{id: "s2", cgroup: cgroup}

		usage, err := session.Usage()
		Expect(err).NotTo(HaveOccurred())
		Expect(usage.CPU).To(Equal(52192 * time.Microsecond))
		Expect(usage.MemoryPeakBytes).To(Equal(uint64(22315008)))

		Expect(session.Kill()).To(Succeed())
		Expect(os.ReadFile(filepath.Join(cgroup, cgroupKillFile))).To(Equal([]byte(killSignal)))

		Expect(os.Remove(filepath.Join(cgroup, cgroupKillFile))).To(Succeed())
		Expect(os.Remove(filepath.Join(cgroup, cpuStatFile))).To(Succeed())
		Expect(os.Remove(filepath.Join(cgroup, memoryPeakFile))).To(Succeed())
		Expect(session.Remove()).To(Succeed())
		Expect(session.Cgroup()).NotTo(BeADirectory())
	})

	It("treats killing and removing an absent cgroup as done", func() {
		session := &Session{id: "gone", cgroup: filepath.Join(sessionsRoot, "gone")}
		Expect(session.Kill()).To(Succeed())
		Expect(session.Remove()).To(Succeed())
	})
})
