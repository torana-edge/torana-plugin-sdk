package plugin_sdk

// ABIMajor remains one throughout this unreleased platform cutover.
const ABIMajor uint32 = 1

// ContractRevision identifies the sole supported wire and semantic contract.
// It is independent of package releases. Incompatible artifacts must rebuild.
const ContractRevision uint32 = 1

const ABIVersion uint64 = uint64(ABIMajor)<<32 | uint64(ContractRevision)
