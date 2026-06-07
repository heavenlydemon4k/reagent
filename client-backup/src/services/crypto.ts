// Decision Stack — Encryption helpers for local storage
// Uses react-native-aes-crypto for AES-256-GCM operations
// SQLCipher key is stored in Expo SecureStore (Keychain/Keystore)

import Aes from 'react-native-aes-crypto';
import * as SecureStore from 'expo-secure-store';

const KEY_ALIAS = 'ds_sqlcipher_key';
const ENCRYPTION_PREFIX = 'enc:v1:';

/**
 * Generate a cryptographically secure random key for SQLCipher.
 * 64 hex characters = 256 bits of entropy.
 */
export async function generateEncryptionKey(): Promise<string> {
  return await Aes.randomKey(32); // 32 bytes = 64 hex chars
}

/**
 * Retrieve the SQLCipher encryption key from secure storage.
 * Generates and stores a new one if none exists.
 */
export async function getOrCreateEncryptionKey(): Promise<string> {
  const existing = await SecureStore.getItemAsync(KEY_ALIAS);
  if (existing) {
    return existing;
  }

  const key = await generateEncryptionKey();
  await SecureStore.setItemAsync(KEY_ALIAS, key, {
    requireAuthentication: false,
    keychainAccessible: SecureStore.WHEN_UNLOCKED,
  });
  return key;
}

/**
 * Delete the stored encryption key (e.g., on logout / remote wipe).
 */
export async function deleteEncryptionKey(): Promise<void> {
  await SecureStore.deleteItemAsync(KEY_ALIAS);
}

/**
 * Encrypt a string value for local storage using AES-256-GCM.
 * Returns: enc:v1:<cipher>:<iv>:<tag>
 */
export async function encryptValue(
  plaintext: string,
  key: string
): Promise<string> {
  const iv = await Aes.randomKey(16); // 16 bytes = 128-bit IV
  const result = await Aes.encrypt(plaintext, key, iv, 'aes-256-gcm');
  return `${ENCRYPTION_PREFIX}${result}:${iv}`;
}

/**
 * Decrypt a string value from local storage.
 * Expects format: enc:v1:<cipher>:<iv>:<tag>
 */
export async function decryptValue(
  ciphertext: string,
  key: string
): Promise<string> {
  if (!ciphertext.startsWith(ENCRYPTION_PREFIX)) {
    // Not encrypted — return as-is (migration path)
    return ciphertext;
  }

  const payload = ciphertext.slice(ENCRYPTION_PREFIX.length);
  const lastColon = payload.lastIndexOf(':');
  if (lastColon === -1) {
    throw new Error('Invalid encrypted payload format');
  }

  const encryptedData = payload.slice(0, lastColon);
  const iv = payload.slice(lastColon + 1);

  return await Aes.decrypt(encryptedData, key, iv, 'aes-256-gcm');
}

/**
 * Hash a string with PBKDF2 for local integrity checks.
 */
export async function hashValue(
  input: string,
  salt: string,
  iterations = 10000
): Promise<string> {
  return await Aes.pbkdf2(input, salt, iterations, 256, 'sha256');
}

/**
 * Generate a device ID for sync protocol.
 */
export async function generateDeviceId(): Promise<string> {
  const key = await getOrCreateEncryptionKey();
  const timestamp = Date.now().toString();
  return await hashValue(timestamp, key.slice(0, 32));
}
