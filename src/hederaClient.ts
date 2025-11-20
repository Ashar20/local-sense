import { Client, AccountId, PrivateKey } from "@hashgraph/sdk";
import { HEDERA_ACCOUNT_ID, HEDERA_PRIVATE_KEY } from "./env";

export function createHederaClient(): Client {
  const operatorId = AccountId.fromString(HEDERA_ACCOUNT_ID);
  // ECDSA key from portal.hedera.com (HEX Encoded Private Key)
  const operatorKey = PrivateKey.fromStringECDSA(HEDERA_PRIVATE_KEY);

  const client = Client.forTestnet();
  client.setOperator(operatorId, operatorKey);

  return client;
}
