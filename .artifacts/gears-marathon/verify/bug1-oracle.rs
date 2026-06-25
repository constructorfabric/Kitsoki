// RED-check harness for bug1 / gh-4115. Public-API only; mirrors the real oracle.
use toolkit::bootstrap::config::AppConfig;
use std::path::PathBuf;

#[test]
fn redcheck_gh4115_dashed_gear_env_override() {
    let tmp = tempfile::tempdir().unwrap();
    let cfg_path = tmp.path().join("cfg.yaml");
    let yaml = "server:\n  home_dir: \"~/.test_gh4115\"\ngears:\n  static-authz-plugin:\n    config:\n      vendor: \"constructorfabric\"\n      priority: 100\n";
    std::fs::write(&cfg_path, yaml).unwrap();
    temp_env::with_var("APP__GEARS__STATIC_AUTHZ_PLUGIN__CONFIG__PRIORITY", Some("50"), || {
        let config = AppConfig::load_layered(&PathBuf::from(&cfg_path)).expect("load ok");
        let p = config.gears.get("static-authz-plugin").expect("gear present")
            .get("config").and_then(|c| c.get("priority")).and_then(|p| p.as_i64()).expect("priority");
        assert_eq!(p, 50, "env override should have applied (RED at baseline if 100)");
    });
}
