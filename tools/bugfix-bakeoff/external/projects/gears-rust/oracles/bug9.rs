// Hidden oracle for bug9 (fix aba9d854): duplicate YAML mapping keys must be rejected.
use modkit::bootstrap::config::AppConfig;
use std::path::PathBuf;

#[test]
fn oracle_bug9_rejects_duplicate_module_keys() {
    let tmp = tempfile::tempdir().unwrap();
    let cfg_path = tmp.path().join("cfg.yaml");
    let yaml = "server:\n  home_dir: \"~/.test_dup\"\nmodules:\n  module1:\n    config: {}\n  module2:\n    config: {}\n  module1:\n    config: {}\n";
    std::fs::write(&cfg_path, yaml).unwrap();
    let result = AppConfig::load_layered(&PathBuf::from(&cfg_path));
    assert!(result.is_err(), "duplicate module names should be rejected");
    let msg = format!("{:?}", result.unwrap_err());
    assert!(msg.to_lowercase().contains("duplicate"), "error should mention duplicates: {msg}");
}
