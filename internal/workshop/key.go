package workshop

// signingPublicKey is the Open Workshop's index signing key (Ed25519, the
// base64 of its public key file: `openssl pkey -in key.pem -pubout`). Its
// private half is the repository secret WORKSHOP_SIGNING_KEY_2 (during the
// change of keys below) and nowhere else; an index without a valid
// signature is refused.
const signingPublicKey = "MCowBQYDK2VwAyEAinWxnNhxTEMVyYghF4FZk5UiJqfPVtWUeJpWxkZqZAw="

// previousPublicKey is the key the index was signed with before, still
// trusted while copies of ppgmods that only know it update: the workflow
// signs the index with both (index.sig with WORKSHOP_SIGNING_KEY, the old
// key; index.sig2 with WORKSHOP_SIGNING_KEY_2, the new one). Once they have
// updated: put the new private key in WORKSHOP_SIGNING_KEY, delete
// WORKSHOP_SIGNING_KEY_2, and remove this key.
const previousPublicKey = "GVSJqtKqZ1ifxuUiuo2tXzsKUVRjYyRQOsilg5NLc6s="
