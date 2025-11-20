import { PI_SENSOR_URL } from "./env";
import { MetricsResponse } from "./types";

export async function fetchMetrics(): Promise<MetricsResponse> {
  const base = PI_SENSOR_URL.replace(/\/$/, "");
  const url = `${base}/metrics`;

  const res = await fetch(url);
  if (!res.ok) {
    throw new Error(`Pi metrics call failed: ${res.status} ${res.statusText}`);
  }

  const data = (await res.json()) as MetricsResponse;

  if (typeof data.ts !== "number" || typeof data.brightness !== "number") {
    throw new Error(`Unexpected metrics shape: ${JSON.stringify(data)}`);
  }

  return data;
}
