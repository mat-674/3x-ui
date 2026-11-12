import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import LogModal from '@/pages/index/LogModal';
import { HttpUtil, FileManager, Msg } from '@/utils';
import enUS from '../../../internal/web/translation/en-US.json';
import { renderWithProviders } from './test-utils';

const T = enUS.pages.index;

interface MockOptions {
  sources?: unknown;
  sourcesError?: string;
  logLevelError?: string;
  logs?: string[];
}

function sourcesFixture(): unknown {
  return [
    { name: 'app', path: '/var/log/x-ui/3xui.log', exists: true, size: 1_258_291 },
    { name: 'edge-access', path: '/etc/x-ui/edge/logs/access.log', exists: true, size: 2400 },
    { name: 'edge-error', path: '/etc/x-ui/edge/logs/error.log', exists: false, size: 0 },
  ];
}

function mockApi(options: MockOptions = {}) {
  vi.spyOn(HttpUtil, 'get').mockImplementation(async (url: string) => {
    if (url.endsWith('/panel/api/server/logs/sources')) {
      return options.sourcesError
        ? new Msg(false, options.sourcesError)
        : new Msg(true, '', options.sources ?? sourcesFixture());
    }
    return new Msg(true, '', null);
  });
  vi.spyOn(HttpUtil, 'post').mockImplementation(async (url: string, body?: unknown) => {
    if (url.endsWith('/panel/api/server/logLevel')) {
      if (options.logLevelError) return new Msg(false, options.logLevelError);
      const level = (body as { level?: string } | undefined)?.level ?? '';
      return new Msg(true, 'success', { level });
    }
    return new Msg(true, 'success', options.logs ?? []);
  });
}

function renderModal() {
  renderWithProviders(<LogModal open onClose={vi.fn()} />);
}

/* AntD renders its choices in a portal, so the option is found by its visible
   label rather than inside the trigger element. */
function chooseOption(selectLabel: string, optionLabel: string) {
  const combobox = screen.getByRole('combobox', { name: selectLabel });
  const select = combobox.closest('.ant-select') as HTMLElement | null;
  if (!select) throw new Error(`select not found for ${selectLabel}`);
  fireEvent.mouseDown(select.querySelector('.ant-select-selector') ?? select);
  const option = Array.from(document.querySelectorAll('.ant-select-item-option')).find(
    (item) => (item.getAttribute('title') ?? item.textContent ?? '').trim() === optionLabel,
  );
  if (!option) throw new Error(`option ${optionLabel} not found for ${selectLabel}`);
  fireEvent.click(option);
}

function postBodies(suffix: string): unknown[] {
  return vi
    .mocked(HttpUtil.post)
    .mock.calls.filter(([url]) => String(url).endsWith(suffix))
    .map(([, body]) => body);
}

/* The download is a real anchor click, so the test observes the URL the browser
   would fetch instead of the DOM the click would have replaced. */
function spyDownloads(): string[] {
  const hrefs: string[] = [];
  vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(function (
    this: HTMLAnchorElement,
  ) {
    hrefs.push(this.getAttribute('href') ?? '');
  });
  return hrefs;
}

function downloadButton(sourceTestId: string, sourceLabel: string): HTMLButtonElement {
  const row = screen.getByTestId(`log-source-${sourceTestId}`);
  return within(row).getByRole('button', {
    name: T.downloadSource.replace('{source}', sourceLabel),
  }) as HTMLButtonElement;
}

afterEach(() => {
  vi.restoreAllMocks();
});

describe('LogModal panel log level', () => {
  it('posts the level the operator picks, because the file keeps DEBUG either way', async () => {
    mockApi();

    renderModal();
    await screen.findByTestId('log-sources');

    // The verbosity is console/syslog only: that sentence is the point of the control.
    expect(screen.getByText(T.panelLogLevelHint)).toBeTruthy();

    chooseOption(T.panelLogLevel, T.logLevelDebug);

    await waitFor(() =>
      expect(postBodies('/panel/api/server/logLevel')).toEqual([{ level: 'debug' }]),
    );
  });

  it('keeps the view filter separate from the panel level', async () => {
    mockApi();

    renderModal();
    await screen.findByTestId('log-sources');

    chooseOption(T.viewLevel, T.logLevelError);

    // The view filter narrows the fetched lines; it must not touch the process level.
    await waitFor(() =>
      expect(postBodies('/panel/api/server/logs/20')).toContainEqual({
        level: 'err',
        syslog: false,
      }),
    );
    expect(postBodies('/panel/api/server/logLevel')).toEqual([]);
  });

  it('renders the backend refusal verbatim when the level is rejected', async () => {
    const refusal = 'unknown log level "verbose": use debug, info, notice, warning or error';
    mockApi({ logLevelError: refusal });

    renderModal();
    await screen.findByTestId('log-sources');

    chooseOption(T.panelLogLevel, T.logLevelWarning);

    const error = await screen.findByTestId('log-level-error');
    expect(error.textContent).toContain(refusal);
    // A refused change left the panel at its previous level, so the select shows it.
    const select = screen.getByRole('combobox', { name: T.panelLogLevel }).closest('.ant-select');
    expect(select?.querySelector('.ant-select-content')?.textContent).toBe(T.logLevelInfo);
  });
});

describe('LogModal log file download', () => {
  it('downloads the tail of a file that exists for the requested number of lines', async () => {
    mockApi();
    const hrefs = spyDownloads();

    renderModal();
    await screen.findByTestId('log-sources');

    fireEvent.click(downloadButton('app', T.logSourceApp));

    expect(hrefs).toEqual(['/panel/api/server/logs/export?source=app&lines=5000']);
  });

  it('refuses a log file that has not been written yet', async () => {
    mockApi();
    const hrefs = spyDownloads();

    renderModal();
    const row = await screen.findByTestId('log-source-edge-error');

    const missing = downloadButton('edge-error', T.logSourceEdgeError);
    expect(missing.hasAttribute('disabled')).toBe(true);
    expect(within(row).getByTestId('log-source-missing-edge-error').textContent).toBe(
      T.logSourceMissingTag,
    );

    // A disabled source must not start a download even if the click gets through.
    fireEvent.click(missing);
    expect(hrefs).toEqual([]);

    // The written files stay downloadable, or the guard proves nothing.
    expect(downloadButton('edge-access', T.logSourceEdgeAccess).hasAttribute('disabled')).toBe(
      false,
    );
  });

  it('shows each file with its size', async () => {
    mockApi();

    renderModal();

    const app = await screen.findByTestId('log-source-app');
    expect(app.textContent).toContain('/var/log/x-ui/3xui.log');
    expect(app.textContent).toContain('1.20 MB');
    expect(screen.getByTestId('log-source-edge-access').textContent).toContain('2.34 KB');
  });

  it('reports a failed source list instead of an empty download list', async () => {
    mockApi({ sourcesError: 'log folder unreadable' });

    renderModal();

    const failed = await screen.findByTestId('log-sources-error');
    expect(failed.textContent).toContain(T.logSourceLoadFailed);
  });
});

describe('LogModal existing behaviour', () => {
  it('still shows the live lines and saves the visible buffer', async () => {
    const lines = [
      '2026/11/12 10:00:00 INFO - panel started',
      '2026/11/12 10:00:01 ERROR - xray exited',
    ];
    mockApi({ logs: lines });
    const save = vi.spyOn(FileManager, 'downloadTextFile').mockImplementation(() => undefined);

    renderModal();

    expect(await screen.findByText(/xray exited/)).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: T.saveVisibleLogs }));

    expect(save).toHaveBeenCalledWith(lines.join('\n'), 'x-ui.log');
  });

  it('keeps sending the syslog flag with the log query', async () => {
    mockApi();

    renderModal();
    await screen.findByTestId('log-sources');

    fireEvent.click(screen.getByRole('checkbox', { name: 'SysLog' }));

    await waitFor(() =>
      expect(postBodies('/panel/api/server/logs/20')).toContainEqual({
        level: 'info',
        syslog: true,
      }),
    );
  });
});
