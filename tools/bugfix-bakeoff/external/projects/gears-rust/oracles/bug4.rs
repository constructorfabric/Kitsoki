// Hidden oracle for bug4 (fix e21d79ab): From impls for common library errors.
use cf_modkit_errors::CanonicalError;

#[test]
fn oracle_bug4_from_io_is_internal_500() {
    let e = CanonicalError::from(std::io::Error::new(std::io::ErrorKind::NotFound, "missing"));
    assert_eq!(e.status_code(), 500);
    assert_eq!(e.title(), "Internal");
}
#[test]
fn oracle_bug4_from_serde_json_is_invalid_argument_400() {
    let je = serde_json::from_str::<serde_json::Value>("not json").unwrap_err();
    let msg = je.to_string();
    let e = CanonicalError::from(je);
    assert_eq!(e.status_code(), 400);
    assert_eq!(e.title(), "Invalid Argument");
    assert_eq!(e.detail(), msg);
}
