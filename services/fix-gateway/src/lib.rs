//! FIX 4.4 gateway library: codec + session state machine. Kept as a lib so the
//! protocol logic is unit-tested independently of the transport (src/main.rs).
pub mod fix;
pub mod session;
pub mod bridge;
