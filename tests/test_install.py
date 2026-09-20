from pathlib import Path
import runpy
import tempfile
import tomllib
import unittest
from unittest.mock import patch

INSTALLER = Path(__file__).resolve().parents[1] / "bin/install-violin-agents"


class InstallTests(unittest.TestCase):
    def test_idempotent_install_preserves_user_settings(self):
        module = runpy.run_path(str(INSTALLER))
        with tempfile.TemporaryDirectory() as tmp, patch('builtins.print'):
            home = Path(tmp)
            (home / '.codex').mkdir()
            config = home / '.codex/config.toml'
            config.write_text('model = "existing-model"\n')
            guidance = home / '.codex/AGENTS.md'
            guidance.write_text('Existing guidance\n')
            with patch.object(Path, 'home', return_value=home):
                module['main']()
                first = config.read_text(), guidance.read_text()
                module['main']()
            self.assertEqual(first, (config.read_text(), guidance.read_text()))
            self.assertEqual(tomllib.loads(first[0])['model'], 'existing-model')
            self.assertTrue(first[1].startswith('Existing guidance\n'))
            self.assertTrue((home/'bin/violin-worker').is_symlink())
            self.assertTrue((home/'bin/violin-agent').is_symlink())
            self.assertTrue((home/'bin/violin-claude').is_symlink())

    def test_unmanaged_server_is_not_overwritten(self):
        module = runpy.run_path(str(INSTALLER))
        with tempfile.TemporaryDirectory() as tmp:
            home = Path(tmp)
            (home/'.codex').mkdir()
            config = home/'.codex/config.toml'
            old = '[mcp_servers.violin_agents]\ncommand = "existing"\n'
            config.write_text(old)
            with patch.object(Path, 'home', return_value=home), self.assertRaises(ValueError):
                module['main']()
            self.assertEqual(config.read_text(), old)
