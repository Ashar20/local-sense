import { TopicMessageSubmitTransaction, TopicId } from "@hashgraph/sdk";
import { createHederaClient } from "./hederaClient";
import { fetchMetrics } from "./piClient";
import { HEDERA_ACCOUNT_ID, LOG_TOPIC_ID } from "./env";

async function main() {
  console.log("=== LocalSense Hedera Bridge (Testnet) ===");
  console.log("Operator account:", HEDERA_ACCOUNT_ID);
  console.log("LOG_TOPIC_ID:", LOG_TOPIC_ID || "<not set>");
  console.log("");

  if (!LOG_TOPIC_ID) {
    throw new Error("LOG_TOPIC_ID is not set in .env – set a valid TopicId on testnet.");
  }

  const client = createHederaClient();

  // Sanity check: ping testnet
  await client.ping(HEDERA_ACCOUNT_ID);
  console.log("✅ Hedera testnet ping OK");

  // 1) Get metrics from Raspberry Pi
  const metrics = await fetchMetrics();
  console.log("📥 Metrics from Pi:", metrics);

  const isoTime = new Date(metrics.ts * 1000).toISOString();

  const messagePayload = {
    ts: metrics.ts,
    ts_iso: isoTime,
    brightness: metrics.brightness,
    source: "localsense-pi-1",
    kind: "brightness_sample"
  };

  const messageJson = JSON.stringify(messagePayload);
  console.log("🧾 Message payload:", messageJson);

  // 2) Prepare + EXECUTE a TopicMessageSubmitTransaction on TESTNET
  const topicId = TopicId.fromString(LOG_TOPIC_ID);

  const tx = await new TopicMessageSubmitTransaction()
    .setTopicId(topicId)
    .setMessage(messageJson)
    .freezeWith(client);

  const txResponse = await tx.execute(client);
  const receipt = await txResponse.getReceipt(client);

  console.log("✅ Submitted message to testnet.");
  console.log("Status:", receipt.status.toString());
  console.log("Transaction ID:", txResponse.transactionId.toString());

  console.log("Done.");
}

main().catch((err) => {
  console.error("❌ Fatal error in bridge:", err);
  process.exit(1);
});
