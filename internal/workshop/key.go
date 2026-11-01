package workshop

// signingPublicKey is the Open Workshop's index signing key (Ed25519, base64
// of the raw public key). Its private half is the repository secret
// WORKSHOP_SIGNING_KEY and nowhere else; an index without a valid signature
// is refused.
const signingPublicKey = "GVSJqtKqZ1ifxuUiuo2tXzsKUVRjYyRQOsilg5NLc6s="
