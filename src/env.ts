import "dotenv/config";

function getEnv(name: string, required = true): string | undefined {
  const value = process.env[name];
  if (required && (!value || value.trim() === "")) {
    if (required) {
      throw new Error(`Missing required env var: ${name}`);
    }
  }
  return value;
}

export const HEDERA_ACCOUNT_ID = getEnv("HEDERA_ACCOUNT_ID") as string;
export const HEDERA_PRIVATE_KEY = getEnv("HEDERA_PRIVATE_KEY") as string;
export const PI_SENSOR_URL =
  (getEnv("PI_SENSOR_URL", false) as string) || "http://192.168.29.121:8000";
export const LOG_TOPIC_ID = getEnv("LOG_TOPIC_ID", false);
