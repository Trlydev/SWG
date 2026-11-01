package workshop

// signingPublicKey is the Open Workshop's index signing key (Ed25519, base64
// of the raw public key). Its private half is the repository secret
// WORKSHOP_SIGNING_KEY and nowhere else; an index without a valid signature
// is refused. Every ppgmods release trusts this key, so the index stays
// readable by old copies too.
const signingPublicKey = "GVSJqtKqZ1ifxuUiuo2tXzsKUVRjYyRQOsilg5NLc6s="

// spareKey is a second key, kept for a later key change (0.4.3 and newer
// trust it). Its private half goes in WORKSHOP_SIGNING_KEY_2; the workflow
// then also writes index.sig2.
//
// To change keys later without breaking older copies: release with the new
// public key here, wait until copies have updated, then put its private
// key in WORKSHOP_SIGNING_KEY.
const spareKey = "MCowBQYDK2VwAyEAinWxnNhxTEMVyYghF4FZk5UiJqfPVtWUeJpWxkZqZAw="
