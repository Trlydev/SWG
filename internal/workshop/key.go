package workshop

// signingPublicKey is the Open Workshop's index signing key (Ed25519, the
// base64 of its public key file: `openssl pkey -in key.pem -pubout`). Its
// private half is the repository secret WORKSHOP_SIGNING_KEY and nowhere
// else; an index without a valid signature is refused.
const signingPublicKey = "MCowBQYDK2VwAyEAinWxnNhxTEMVyYghF4FZk5UiJqfPVtWUeJpWxkZqZAw="

// previousPublicKey signed the index until the owner's key replaced it
// (2026-10-31). It stays trusted only so the index published with it before
// the switch is accepted; drop it once the index is signed with the new
// key.
//
// To change keys later without breaking older copies: put the new private
// key in WORKSHOP_SIGNING_KEY_2 (the workflow then also writes index.sig2),
// add its public key here, release, and once copies have updated move it
// into WORKSHOP_SIGNING_KEY and delete WORKSHOP_SIGNING_KEY_2.
const previousPublicKey = "GVSJqtKqZ1ifxuUiuo2tXzsKUVRjYyRQOsilg5NLc6s="
