export interface Client {
  send(payload: string): Promise<string>;
}

export class HttpClient implements Client {
  constructor(private readonly baseUrl: string) {}

  async send(payload: string): Promise<string> {
    const res = await fetch(`${this.baseUrl}/api`, {
      method: "POST",
      body: payload,
    });
    return res.text();
  }
}

async function main(): Promise<void> {
  const client: Client = new HttpClient("http://localhost:8080");
  const response = await client.send("ping");
  console.log(response);
}

main();
