pub(crate) const ABI_VERSION: u32 = 1;

#[unsafe(no_mangle)]
pub extern "C" fn mcgo_engine_abi_version() -> u32 {
    ABI_VERSION
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn exported_version_is_one() {
        assert_eq!(mcgo_engine_abi_version(), 1);
    }
}
