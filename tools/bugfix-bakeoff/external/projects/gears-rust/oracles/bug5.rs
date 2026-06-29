// Hidden oracle for bug5 (fix 8737281d): membership type codes must NOT require the RG prefix.
use cyberware_resource_group::domain::error::DomainError;
use cyberware_resource_group::domain::validation::{self, RG_TYPE_PREFIX};

#[test]
fn oracle_bug5_membership_accepts_external_and_rejects_empty() {
    assert!(validation::validate_membership_type_code("gts.cf.core.idp.user.v1~").is_ok());
    let code = format!("{RG_TYPE_PREFIX}y.core.tn.tenant.v1~");
    assert!(validation::validate_membership_type_code(&code).is_ok());
    assert!(matches!(validation::validate_membership_type_code("").unwrap_err(), DomainError::Validation { .. }));
}
