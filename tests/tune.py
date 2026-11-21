#!/usr/bin/env python3
"""Isolated regression tests: all paths, sysctls and module loads are fixtures."""
import fcntl
import json
import os
from pathlib import Path
import shlex
import subprocess
import sys
import tempfile
import unittest


SCRIPT = Path(__file__).resolve().parents[1] / "slacker-tune.sh"
BASE = {
    "net.ipv4.tcp_available_congestion_control": "reno cubic bbr",
    "net.core.default_qdisc": "fq_codel",
    "net.ipv4.tcp_congestion_control": "cubic",
    "net.core.rmem_max": "212992",
    "net.core.wmem_max": "212992",
    "net.ipv4.tcp_rmem": "4096 87380 6291456",
    "net.ipv4.tcp_wmem": "4096 16384 4194304",
    "net.ipv4.tcp_moderate_rcvbuf": "0",
    "net.ipv4.tcp_mtu_probing": "0",
    "net.core.somaxconn": "128",
    "net.ipv4.tcp_max_syn_backlog": "1024",
    "net.ipv4.tcp_syncookies": "1",
    "net.core.netdev_max_backlog": "1000",
    "net.ipv4.tcp_fin_timeout": "60",
    "net.ipv4.tcp_keepalive_time": "7200",
    "net.ipv4.tcp_keepalive_intvl": "75",
    "net.ipv4.tcp_keepalive_probes": "9",
    "net.ipv4.udp_rmem_min": "4096",
    "net.ipv4.udp_wmem_min": "4096",
}
OLD_BBR = """# Slacker TCP BBR tuning. Managed by slacker-bbr.sh.
net.core.default_qdisc = fq
net.ipv4.tcp_congestion_control = bbr
net.core.rmem_max = 33554432
net.core.wmem_max = 33554432
net.ipv4.tcp_rmem = 4096 131072 33554432
net.ipv4.tcp_wmem = 4096 131072 33554432
net.ipv4.tcp_mtu_probing = 1
"""
OLD_NET = """# Slacker conservative network tuning; independent of 99-slacker-bbr.conf
net.core.somaxconn = 4096
net.core.netdev_max_backlog = 4096
net.ipv4.tcp_max_syn_backlog = 8192
net.ipv4.tcp_syncookies = 1
net.ipv4.tcp_fin_timeout = 30
net.ipv4.tcp_keepalive_time = 600
net.ipv4.tcp_keepalive_intvl = 30
net.ipv4.tcp_keepalive_probes = 5
net.ipv4.tcp_mtu_probing = 1
net.ipv4.udp_rmem_min = 16384
net.ipv4.udp_wmem_min = 16384
"""

MOCK_SYSCTL = r'''
import json, os, pathlib, sys
root = pathlib.Path(os.environ["TUNE_FIXTURE"])
data = json.loads((root / "values.json").read_text())
faults = json.loads((root / "faults.json").read_text())
args = sys.argv[1:]
if args[:1] == ["-w"]:
    key, value = args[1].split("=", 1)
    with (root / "writes.log").open("a") as log:
        log.write(key + "=" + value + "\n")
    if value in faults.get("fail_writes", {}).get(key, []):
        sys.exit("fixture: write rejected: " + key)
    if value not in faults.get("ignore_writes", {}).get(key, []):
        data[key] = value
        (root / "values.json").write_text(json.dumps(data))
    print(key + " = " + data[key])
else:
    key = args[-1]
    if key not in data or key in faults.get("fail_reads", []):
        sys.exit("fixture: unavailable: " + key)
    print(data[key] if args[:1] == ["-n"] else key + " = " + data[key])
'''


class TuningTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="slacker-tune-test-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.conf = self.root / "etc/sysctl.d/99-slacker-tune.conf"
        self.modules = self.root / "etc/modules-load.d/slacker-tune.conf"
        self.old_bbr = self.conf.with_name("99-slacker-bbr.conf")
        self.old_net = self.conf.with_name("98-slacker-network.conf")
        self.state = self.root / "var/lib/slacker-tune"
        self.lock = self.root / "run/lock/slacker-tune.lock"
        self.meminfo = self.root / "meminfo"
        self.conf.parent.mkdir(parents=True)
        self.modules.parent.mkdir(parents=True)
        self.meminfo.write_text("MemTotal: 524288 kB\n")
        self.set_values(BASE)
        self.set_faults({})
        fake_bin = self.root / "bin"
        fake_bin.mkdir()
        for name, content in {
            "sysctl": "#!" + sys.executable + "\n" + MOCK_SYSCTL,
            "modprobe": "#!/bin/sh\nexit 0\n",
            "tc": "#!/bin/sh\nprintf 'qdisc fq_codel 0: dev eth0 root\n'\n",
        }.items():
            path = fake_bin / name
            path.write_text(content)
            path.chmod(0o755)
        overrides = {
            "CONF": self.conf, "MODULES": self.modules, "STATE": self.state,
            "LOCK": self.lock, "LEGACY_BBR": self.old_bbr,
            "LEGACY_NET": self.old_net, "MEMINFO": self.meminfo,
        }
        self.wrapper = self.root / "run.sh"
        self.wrapper.write_text(
            "#!/usr/bin/env bash\nsource " + shlex.quote(str(SCRIPT)) + "\n"
            + "\n".join(k + "=" + shlex.quote(str(v)) for k, v in overrides.items())
            + '\nrequire_root() { :; }\nmain "$@"\n'
        )
        self.env = dict(os.environ, TUNE_FIXTURE=str(self.root),
                        PATH=str(fake_bin) + os.pathsep + os.environ["PATH"])

    def set_values(self, values):
        (self.root / "values.json").write_text(json.dumps(values))

    def values(self):
        return json.loads((self.root / "values.json").read_text())

    def set_faults(self, faults):
        (self.root / "faults.json").write_text(json.dumps(faults))

    def run_tune(self, command, success=True):
        result = subprocess.run(["bash", str(self.wrapper), command], env=self.env,
                                text=True, capture_output=True, timeout=20)
        output = result.stdout + result.stderr
        if success:
            self.assertEqual(result.returncode, 0, output)
        else:
            self.assertNotEqual(result.returncode, 0, output)
        return output

    def assert_pristine(self, values=BASE):
        self.assertEqual(self.values(), values)
        self.assertFalse(self.conf.exists())
        self.assertFalse(self.modules.exists())
        self.assertFalse(self.state.exists())

    def state_bytes(self):
        return {str(p.relative_to(self.state)): p.read_bytes()
                for p in self.state.rglob("*") if p.is_file()}

    def write_old_configs(self):
        self.old_bbr.write_text(OLD_BBR)
        self.old_net.write_text(OLD_NET)
        self.old_bbr.chmod(0o640)
        values = dict(BASE)
        for content in (OLD_BBR, OLD_NET):
            for line in content.splitlines():
                if line and not line.startswith("#"):
                    key, value = line.split("=", 1)
                    values[key.strip()] = value.strip()
        self.set_values(values)
        return values

    def test_install_and_restore(self):
        self.run_tune("install")
        tuned = self.values()
        self.assertEqual(tuned["net.ipv4.tcp_congestion_control"], "bbr")
        self.assertEqual(tuned["net.core.default_qdisc"], "fq")
        self.assertEqual(tuned["net.core.rmem_max"], "8388608")
        self.assertEqual(tuned["net.ipv4.tcp_rmem"], "4096 87380 8388608")
        self.assertEqual(tuned["net.ipv4.tcp_wmem"], "4096 16384 8388608")
        self.assertEqual(tuned["net.ipv4.tcp_fin_timeout"], "60")
        self.assertEqual(tuned["net.ipv4.udp_rmem_min"], "4096")
        self.assertIn("tcp_bbr\nsch_fq\n", self.modules.read_text())
        self.assertEqual(self.conf.stat().st_mode & 0o777, 0o644)
        self.run_tune("restore")
        self.assert_pristine()

    def test_repeat_install_preserves_backup_and_does_not_write(self):
        self.run_tune("install")
        saved = self.state_bytes()
        writes = (self.root / "writes.log").read_bytes()
        self.run_tune("install")
        self.assertEqual(self.state_bytes(), saved)
        self.assertEqual((self.root / "writes.log").read_bytes(), writes)
        self.run_tune("restore")
        self.assert_pristine()

    def test_memory_tiers_and_larger_existing_limits(self):
        for memory_kib, expected in ((2097152, "16777216"), (8388608, "33554432")):
            with self.subTest(memory_kib=memory_kib):
                self.meminfo.write_text(f"MemTotal: {memory_kib} kB\n")
                self.run_tune("install")
                self.assertEqual(self.values()["net.core.wmem_max"], expected)
                self.run_tune("restore")
        larger = dict(BASE, **{
            "net.core.rmem_max": "67108864", "net.core.wmem_max": "67108864",
            "net.ipv4.tcp_rmem": "8192 262144 134217728",
            "net.ipv4.tcp_wmem": "8192 65536 67108864",
            "net.core.somaxconn": "65535", "net.ipv4.tcp_max_syn_backlog": "32768",
            "net.ipv4.tcp_mtu_probing": "2", "net.ipv4.tcp_syncookies": "2",
        })
        self.set_values(larger)
        self.run_tune("install")
        for key in ("net.core.rmem_max", "net.core.wmem_max", "net.ipv4.tcp_rmem",
                    "net.ipv4.tcp_wmem", "net.core.somaxconn", "net.ipv4.tcp_max_syn_backlog",
                    "net.ipv4.tcp_mtu_probing", "net.ipv4.tcp_syncookies"):
            self.assertEqual(self.values()[key], larger[key])
        self.run_tune("restore")
        self.assert_pristine(larger)

    def test_bbr_unavailable_is_preflight_failure(self):
        values = dict(BASE, **{"net.ipv4.tcp_available_congestion_control": "reno cubic"})
        self.set_values(values)
        self.run_tune("install", success=False)
        self.assert_pristine(values)
        self.assertFalse((self.root / "writes.log").exists())

    def test_missing_required_sysctl_is_preflight_failure(self):
        self.set_faults({"fail_reads": ["net.ipv4.tcp_wmem"]})
        self.run_tune("install", success=False)
        self.assert_pristine()
        self.assertFalse((self.root / "writes.log").exists())
        self.assertEqual(list(self.state.parent.glob("*.stage.*")), [])

    def test_partial_apply_failure_rolls_back(self):
        self.set_faults({"fail_writes": {"net.core.somaxconn": ["4096"]}})
        self.run_tune("install", success=False)
        self.assert_pristine()
        self.assertIn("net.ipv4.tcp_congestion_control=bbr", (self.root / "writes.log").read_text())

    def test_readback_mismatch_rolls_back(self):
        self.set_faults({"ignore_writes": {"net.core.rmem_max": ["8388608"]}})
        self.run_tune("install", success=False)
        self.assert_pristine()

    def test_incomplete_rollback_keeps_backup_for_retry(self):
        self.set_faults({"fail_writes": {
            "net.core.somaxconn": ["4096"],
            "net.ipv4.tcp_congestion_control": ["cubic"],
        }})
        self.run_tune("install", success=False)
        self.assertEqual(self.values()["net.ipv4.tcp_congestion_control"], "bbr")
        self.assertTrue((self.state / "runtime.conf").exists())
        baseline = (self.state / "runtime.conf").read_bytes()
        self.run_tune("install", success=False)
        self.assertEqual((self.state / "runtime.conf").read_bytes(), baseline)
        self.set_faults({})
        self.run_tune("restore")
        self.assert_pristine()

    def test_failed_restore_preserves_backup_for_retry(self):
        self.run_tune("install")
        baseline = (self.state / "runtime.conf").read_bytes()
        self.set_faults({"fail_writes": {"net.ipv4.tcp_congestion_control": ["cubic"]}})
        self.run_tune("restore", success=False)
        self.assertEqual((self.state / "runtime.conf").read_bytes(), baseline)
        self.assertEqual((self.state / "phase").read_text().strip(), "restoring")
        self.set_faults({})
        self.run_tune("restore")
        self.assert_pristine()

    def test_migration_preserves_current_legacy_choices_and_original_backups(self):
        values = self.write_old_configs()
        values["net.ipv4.tcp_keepalive_time"] = "900"
        self.set_values(values)
        legacy_backups = self.state.parent / "slacker-bbr"
        legacy_backups.mkdir(parents=True)
        (legacy_backups / "previous.conf").write_text("original snapshot\n")
        self.run_tune("install")
        self.assertFalse(self.old_bbr.exists())
        self.assertFalse(self.old_net.exists())
        self.assertIn("net.ipv4.tcp_keepalive_time = 900", self.conf.read_text())
        self.assertEqual((legacy_backups / "previous.conf").read_text(), "original snapshot\n")
        self.run_tune("restore")
        self.assert_pristine(values)
        self.assertEqual(self.old_bbr.read_text(), OLD_BBR)
        self.assertEqual(self.old_net.read_text(), OLD_NET)
        self.assertEqual(self.old_bbr.stat().st_mode & 0o777, 0o640)

    def test_failed_migration_restores_old_configs(self):
        values = self.write_old_configs()
        values["net.ipv4.tcp_moderate_rcvbuf"] = "0"
        self.set_values(values)
        self.set_faults({"fail_writes": {"net.ipv4.tcp_moderate_rcvbuf": ["1"]}})
        self.run_tune("install", success=False)
        self.assert_pristine(values)
        self.assertEqual(self.old_bbr.read_text(), OLD_BBR)
        self.assertEqual(self.old_net.read_text(), OLD_NET)

    def test_customized_old_config_is_not_deleted(self):
        custom = OLD_BBR + "net.ipv4.ip_forward = 1\n"
        self.old_bbr.write_text(custom)
        self.run_tune("install", success=False)
        self.assert_pristine()
        self.assertEqual(self.old_bbr.read_text(), custom)
        self.assertFalse((self.root / "writes.log").exists())

    def test_restore_without_install_is_non_destructive(self):
        self.old_bbr.write_text(OLD_BBR)
        self.run_tune("restore", success=False)
        self.assert_pristine()
        self.assertEqual(self.old_bbr.read_text(), OLD_BBR)
        self.assertFalse((self.root / "writes.log").exists())

    def test_status_is_read_only(self):
        before = {p.relative_to(self.root): p.read_bytes()
                  for p in self.root.rglob("*") if p.is_file()}
        self.run_tune("status")
        after = {p.relative_to(self.root): p.read_bytes()
                 for p in self.root.rglob("*") if p.is_file()}
        self.assertEqual(after, before)
        self.assertFalse(self.lock.exists())

    def test_lock_contention_prevents_write(self):
        self.lock.parent.mkdir(parents=True)
        with self.lock.open("w") as lock:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
            self.run_tune("install", success=False)
        self.assert_pristine()
        self.assertFalse((self.root / "writes.log").exists())

    def test_orphan_config_is_not_overwritten(self):
        self.conf.write_text("operator-owned config\n")
        self.run_tune("install", success=False)
        self.assertEqual(self.conf.read_text(), "operator-owned config\n")
        self.assertEqual(self.values(), BASE)
        self.assertFalse(self.state.exists())

    def test_symlink_is_rejected(self):
        target = self.root / "operator.conf"
        target.write_text("operator-owned config\n")
        self.old_bbr.symlink_to(target)
        self.run_tune("install", success=False)
        self.assertEqual(target.read_text(), "operator-owned config\n")
        self.assertTrue(self.old_bbr.is_symlink())
        self.assert_pristine()

    def test_changed_managed_value_does_not_replace_original_backup(self):
        self.run_tune("install")
        saved = self.state_bytes()
        changed = self.values()
        changed["net.core.somaxconn"] = "64"
        self.set_values(changed)
        self.run_tune("install", success=False)
        self.assertEqual(self.state_bytes(), saved)
        self.assertEqual(self.values(), changed)
        self.run_tune("restore")
        self.assert_pristine()

    def assert_corrupt_backup_rejected(self, path, replacement):
        original = path.read_bytes()
        values = self.values()
        active = {p: p.read_bytes() for p in (self.conf, self.modules)}
        phase = (self.state / "phase").read_bytes()
        writes = (self.root / "writes.log").read_bytes()
        path.write_bytes(replacement)
        self.run_tune("restore", success=False)
        self.assertEqual(self.values(), values)
        self.assertEqual({p: p.read_bytes() for p in active}, active)
        self.assertEqual((self.state / "phase").read_bytes(), phase)
        self.assertEqual((self.root / "writes.log").read_bytes(), writes)
        self.assertEqual(path.read_bytes(), replacement)
        self.assertTrue(self.state.is_dir())
        path.write_bytes(original)

    def test_truncated_runtime_backup_blocks_restore_before_changes(self):
        self.run_tune("install")
        path = self.state / "runtime.conf"
        self.assert_corrupt_backup_rejected(path, path.read_bytes().splitlines(keepends=True)[0])
        self.run_tune("restore")
        self.assert_pristine()

    def test_truncated_manifest_blocks_restore_before_changes(self):
        self.run_tune("install")
        path = self.state / "checksums.sha256"
        self.assert_corrupt_backup_rejected(path, b"".join(path.read_bytes().splitlines(keepends=True)[:-1]))
        self.run_tune("restore")
        self.assert_pristine()

    def test_damaged_old_config_backup_blocks_restore_before_changes(self):
        values = self.write_old_configs()
        self.run_tune("install")
        path = self.state / "files/2"
        self.assert_corrupt_backup_rejected(path, path.read_bytes() + b"net.ipv4.ip_forward = 1\n")
        self.assertFalse(self.old_bbr.exists())
        self.assertFalse(self.old_net.exists())
        self.run_tune("restore")
        self.assert_pristine(values)
        self.assertEqual(self.old_bbr.read_text(), OLD_BBR)
        self.assertEqual(self.old_net.read_text(), OLD_NET)


if __name__ == "__main__":
    unittest.main(verbosity=2)
