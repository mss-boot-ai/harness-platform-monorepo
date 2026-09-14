import { connectGateway, type EndpointIdentity, type IndexedDbSecureStore, type ReadyGatewayConnection } from '@harness/hc-core';
import {
  accessCredentialNeedsRefresh, fetchTrustManifest, HcApiError, issueWebSocketTicket, loginBrowserSession,
  refreshEndpointSession, registerBrowserEndpoint, type RegistrationSession,
} from '../api';

/** Construct only inside the installation's Web Lock; every continuation also checks that lease. */
export class EndpointAccess {
  private active = true;
  private registration: RegistrationSession | null = null;
  private refreshing: Promise<RegistrationSession> | null = null;
  private readonly pending = new Set<Promise<unknown>>();
  public constructor(public readonly identity: EndpointIdentity, private readonly owned: () => boolean,
    private readonly changed: (value: RegistrationSession) => void) {}
  public assertOwned(): void {
    if (!this.active || !this.owned()) throw new Error('Endpoint ownership is unavailable');
  }
  private run<T>(operation: () => Promise<T>): Promise<T> {
    try { this.assertOwned(); } catch (cause) { return Promise.reject(cause); }
    const result = operation(); this.pending.add(result);
    void result.then(() => this.pending.delete(result), () => this.pending.delete(result));
    return result;
  }
  private accept(value: RegistrationSession): RegistrationSession {
    this.assertOwned();
    if (value.signingJkt !== this.identity.signing.thumbprint || value.kemJkt !== this.identity.kem.thumbprint ||
      (this.registration !== null && value.endpointId !== this.registration.endpointId)) throw new Error('Refreshed endpoint identity changed');
    this.registration = value; this.changed(value); return value;
  }
  public refresh(): Promise<RegistrationSession> {
    try { this.assertOwned(); } catch (cause) { return Promise.reject(cause); }
    if (this.refreshing !== null) return this.refreshing;
    const result = this.run(async () => this.accept(await refreshEndpointSession(this.identity)));
    this.refreshing = result;
    void result.then(() => { if (this.refreshing === result) this.refreshing = null; }, () => { if (this.refreshing === result) this.refreshing = null; });
    return result;
  }
  public current(): Promise<RegistrationSession> {
    try { this.assertOwned(); } catch (cause) { return Promise.reject(cause); }
    return this.registration === null || accessCredentialNeedsRefresh(this.registration) ? this.refresh() : Promise.resolve(this.registration);
  }
  public login(username: string, password: string): Promise<RegistrationSession> {
    return this.run(async () => {
      await loginBrowserSession(username, password); this.assertOwned();
      return this.accept(await registerBrowserEndpoint(this.identity, 'H5 browser'));
    });
  }
  public authorized<T>(operation: (value: RegistrationSession) => Promise<T>): Promise<T> {
    return this.run(async () => {
      const initial = await this.current(); this.assertOwned();
      try { const value = await operation(initial); this.assertOwned(); return value; }
      catch (cause) {
        this.assertOwned();
        if (!(cause instanceof HcApiError) || cause.code !== 'ENDPOINT_CREDENTIAL_EXPIRED') throw cause;
        const current = this.registration !== initial && this.registration !== null ? this.registration : await this.refresh();
        this.assertOwned(); const value = await operation(current); this.assertOwned(); return value;
      }
    });
  }
  public connect(store: IndexedDbSecureStore): Promise<ReadyGatewayConnection> {
    return this.run(async () => {
      const registration = await this.current(); this.assertOwned();
      const ticket = await this.authorized((value) => issueWebSocketTicket(this.identity, value)); this.assertOwned();
      const trust = await fetchTrustManifest(); this.assertOwned();
      await store.pinTrustRoot(trust.rootJkt, trust.revision, trust.onlineJkt, trust.expiresAt); this.assertOwned();
      const current = await this.current(); this.assertOwned();
      // Ticket issuance may have refreshed the short-lived credential.
      if (current.endpointId !== registration.endpointId) throw new Error('Endpoint changed during connection');
      const ready = await connectGateway(this.identity, { credentialId: current.credentialId, endpointId: current.endpointId,
        ticket: ticket.ticket, websocketUrl: ticket.websocketUrl }, trust);
      try { this.assertOwned(); return ready; }
      catch (cause) { ready.socket.close(1000, 'Endpoint ownership ended'); throw cause; }
    });
  }
  public async dispose(): Promise<void> {
    this.active = false;
    await Promise.allSettled([...this.pending]);
    this.registration = null;
  }
}
