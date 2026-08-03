import { getAssetsDb } from "./assets";

const COOKIE_NAME = "fulibu_session";
const SESSION_SECONDS = 60 * 60 * 24 * 30;
const PASSWORD_ITERATIONS = 210000;
const encoder = new TextEncoder();

export type LocalUser = { id: number; email: string; displayName: string };

type UserRow = {
  id: number;
  email: string;
  display_name: string;
  password_hash: string;
  password_salt: string;
  password_iterations: number;
};

function bytesToBase64(bytes: Uint8Array) {
  let binary = "";
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return btoa(binary);
}

function base64ToBytes(value: string) {
  const binary = atob(value);
  return Uint8Array.from(binary, (character) => character.charCodeAt(0));
}

function randomBase64(length: number) {
  const bytes = new Uint8Array(length);
  crypto.getRandomValues(bytes);
  return bytesToBase64(bytes);
}

async function sha256(value: string) {
  const digest = await crypto.subtle.digest("SHA-256", encoder.encode(value));
  return bytesToBase64(new Uint8Array(digest));
}

async function derivePassword(password: string, salt: string, iterations: number) {
  const key = await crypto.subtle.importKey("raw", encoder.encode(password), "PBKDF2", false, ["deriveBits"]);
  const bits = await crypto.subtle.deriveBits(
    { name: "PBKDF2", hash: "SHA-256", salt: base64ToBytes(salt), iterations },
    key,
    256
  );
  return bytesToBase64(new Uint8Array(bits));
}

function constantTimeEqual(left: string, right: string) {
  const a = encoder.encode(left);
  const b = encoder.encode(right);
  if (a.length !== b.length) return false;
  let mismatch = 0;
  for (let index = 0; index < a.length; index += 1) mismatch |= a[index] ^ b[index];
  return mismatch === 0;
}

function readCookie(request: Request, name: string) {
  const header = request.headers.get("cookie") ?? "";
  for (const part of header.split(";")) {
    const [key, ...rest] = part.trim().split("=");
    if (key === name) return decodeURIComponent(rest.join("="));
  }
  return null;
}

export function sessionCookie(token: string, request: Request) {
  const secure = new URL(request.url).protocol === "https:" ? "; Secure" : "";
  return `${COOKIE_NAME}=${encodeURIComponent(token)}; Path=/; HttpOnly; SameSite=Lax; Max-Age=${SESSION_SECONDS}${secure}`;
}

export function clearSessionCookie(request: Request) {
  const secure = new URL(request.url).protocol === "https:" ? "; Secure" : "";
  return `${COOKIE_NAME}=; Path=/; HttpOnly; SameSite=Lax; Max-Age=0${secure}`;
}

export async function hashNewPassword(password: string) {
  const salt = randomBase64(18);
  return {
    salt,
    iterations: PASSWORD_ITERATIONS,
    hash: await derivePassword(password, salt, PASSWORD_ITERATIONS),
  };
}

export async function verifyPassword(password: string, user: UserRow) {
  const candidate = await derivePassword(password, user.password_salt, user.password_iterations);
  return constantTimeEqual(candidate, user.password_hash);
}

export async function findUserByEmail(email: string) {
  const db = await getAssetsDb();
  const user = await db.prepare(
    "SELECT id, email, display_name, password_hash, password_salt, password_iterations FROM users WHERE email = ?"
  ).bind(email.trim().toLowerCase()).first<UserRow>();
  return { db, user };
}

export async function createSession(db: D1Database, userId: number) {
  const token = randomBase64(32);
  const tokenHash = await sha256(token);
  const expiresAt = Math.floor(Date.now() / 1000) + SESSION_SECONDS;
  await db.batch([
    db.prepare("DELETE FROM sessions WHERE expires_at <= ?").bind(Math.floor(Date.now() / 1000)),
    db.prepare("INSERT INTO sessions (token_hash, user_id, expires_at) VALUES (?, ?, ?)").bind(tokenHash, userId, expiresAt),
  ]);
  return token;
}

export async function getAuthenticatedUser(request: Request): Promise<LocalUser | null> {
  const token = readCookie(request, COOKIE_NAME);
  if (!token) return null;
  const db = await getAssetsDb();
  const tokenHash = await sha256(token);
  const row = await db.prepare(`
    SELECT users.id, users.email, users.display_name
    FROM sessions
    JOIN users ON users.id = sessions.user_id
    WHERE sessions.token_hash = ? AND sessions.expires_at > ?
  `).bind(tokenHash, Math.floor(Date.now() / 1000)).first<{ id: number; email: string; display_name: string }>();
  return row ? { id: row.id, email: row.email, displayName: row.display_name } : null;
}

export async function deleteCurrentSession(request: Request) {
  const token = readCookie(request, COOKIE_NAME);
  if (!token) return;
  const db = await getAssetsDb();
  await db.prepare("DELETE FROM sessions WHERE token_hash = ?").bind(await sha256(token)).run();
}
