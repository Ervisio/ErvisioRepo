import { fromBase64url, toBase64url } from "./base64url.ts";

// The server sends WebAuthn options as JSON with base64url binary fields (the shape of
// PublicKeyCredentialCreationOptionsJSON / RequestOptionsJSON) and expects the credential
// back in the same JSON form.
type Descriptor = { id: string; type: "public-key"; transports?: string[] };
export type CreationOptionsJSON = Omit<PublicKeyCredentialCreationOptions, "challenge" | "user" | "excludeCredentials"> & {
  challenge: string;
  user: { id: string; name: string; displayName: string };
  excludeCredentials?: Descriptor[];
};
export type RequestOptionsJSON = Omit<PublicKeyCredentialRequestOptions, "challenge" | "allowCredentials"> & {
  challenge: string;
  allowCredentials?: Descriptor[];
};

export function webauthnSupported(): boolean {
  return typeof window !== "undefined" && "PublicKeyCredential" in window && !!navigator.credentials;
}

const descriptors = (list?: Descriptor[]): PublicKeyCredentialDescriptor[] | undefined =>
  list?.map((d) => ({ ...d, id: fromBase64url(d.id) }) as PublicKeyCredentialDescriptor);

export async function createCredential(o: CreationOptionsJSON): Promise<Record<string, unknown>> {
  const cred = (await navigator.credentials.create({
    publicKey: {
      ...o,
      challenge: fromBase64url(o.challenge),
      user: { ...o.user, id: fromBase64url(o.user.id) },
      excludeCredentials: descriptors(o.excludeCredentials),
    },
  })) as PublicKeyCredential | null;
  if (!cred) throw new Error("no credential");
  const r = cred.response as AuthenticatorAttestationResponse;
  return {
    id: cred.id,
    rawId: toBase64url(cred.rawId),
    type: cred.type,
    authenticatorAttachment: cred.authenticatorAttachment ?? undefined,
    response: {
      clientDataJSON: toBase64url(r.clientDataJSON),
      attestationObject: toBase64url(r.attestationObject),
      transports: r.getTransports?.() ?? [],
    },
    clientExtensionResults: cred.getClientExtensionResults(),
  };
}

export async function getCredential(o: RequestOptionsJSON): Promise<Record<string, unknown>> {
  const cred = (await navigator.credentials.get({
    publicKey: { ...o, challenge: fromBase64url(o.challenge), allowCredentials: descriptors(o.allowCredentials) },
  })) as PublicKeyCredential | null;
  if (!cred) throw new Error("no credential");
  const r = cred.response as AuthenticatorAssertionResponse;
  return {
    id: cred.id,
    rawId: toBase64url(cred.rawId),
    type: cred.type,
    authenticatorAttachment: cred.authenticatorAttachment ?? undefined,
    response: {
      clientDataJSON: toBase64url(r.clientDataJSON),
      authenticatorData: toBase64url(r.authenticatorData),
      signature: toBase64url(r.signature),
      userHandle: r.userHandle ? toBase64url(r.userHandle) : undefined,
    },
    clientExtensionResults: cred.getClientExtensionResults(),
  };
}
