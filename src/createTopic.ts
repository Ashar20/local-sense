import { Client, TopicCreateTransaction, AccountId, PrivateKey } from "@hashgraph/sdk";
import "dotenv/config";

const accountId = process.env.HEDERA_ACCOUNT_ID;
const privateKey = process.env.HEDERA_PRIVATE_KEY;

if (!accountId || !privateKey) {
  throw new Error("HEDERA_ACCOUNT_ID or HEDERA_PRIVATE_KEY missing in .env");
}

async function main() {
  const client = Client.forTestnet();

  // ECDSA key from portal (HEX Encoded Private Key)
  const operatorId = AccountId.fromString(accountId as string);
  const operatorKey = PrivateKey.fromStringECDSA(privateKey as string);
  client.setOperator(operatorId, operatorKey);

  console.log("Creating new topic on Hedera TESTNET…");

  const tx = await new TopicCreateTransaction()
    .setSubmitKey(operatorKey.publicKey)
    .setAdminKey(operatorKey.publicKey)
    .setTopicMemo("LocalSense brightness logs")
    .freezeWith(client);

  const txResponse = await tx.execute(client);
  const receipt = await txResponse.getReceipt(client);

  const topicId = receipt.topicId?.toString();
  console.log("✅ Topic created.");
  console.log("Topic ID:", topicId);
}

main().catch((err) => {
  console.error("Error creating topic:", err);
  process.exit(1);
});
