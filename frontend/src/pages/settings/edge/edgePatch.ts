import type { AllSetting } from '@/models/setting';

export type EdgePatch = Pick<
  AllSetting,
  | 'edgeEnable'
  | 'edgeListen'
  | 'edgePort'
  | 'edgeServerNames'
  | 'edgeCertFile'
  | 'edgeKeyFile'
  | 'edgeNginxBin'
  | 'edgeProxyProtocol'
>;

/* Apply reads the saved settings, so the form's eight keys are persisted first —
   otherwise the operator watches a confirmed Apply run the previous port. */
export function edgePatchOf(setting: AllSetting): EdgePatch {
  return {
    edgeEnable: setting.edgeEnable,
    edgeListen: setting.edgeListen,
    edgePort: setting.edgePort,
    edgeServerNames: setting.edgeServerNames,
    edgeCertFile: setting.edgeCertFile,
    edgeKeyFile: setting.edgeKeyFile,
    edgeNginxBin: setting.edgeNginxBin,
    edgeProxyProtocol: setting.edgeProxyProtocol,
  };
}
