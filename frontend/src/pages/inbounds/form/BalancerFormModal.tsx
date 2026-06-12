import { useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Alert, Collapse, Form, Input, Modal, Radio, Select, message } from 'antd';

import { HttpUtil } from '@/utils';
import { formatInboundLabel } from '@/lib/inbounds/label';
import { coerceInboundJsonField, type DBInbound } from '@/models/dbinbound';
import { Protocols } from '@/schemas/primitives';

type Visibility = 'all' | 'selected';

interface MemberSub {
  subId: string;
  emails: string[];
}

interface BalancerFormModalProps {
  open: boolean;
  mode: 'add' | 'edit';
  dbInbound: DBInbound | null;
  dbInbounds: DBInbound[];
  onClose: () => void;
  onSaved?: () => void;
}

// Protocols the JSON subscription can emit as outbounds — only these can be
// balancer members. Mirrors balancerEligibleProtocols on the backend.
const ELIGIBLE = new Set<string>([
  Protocols.VMESS,
  Protocols.VLESS,
  Protocols.TROJAN,
  Protocols.SHADOWSOCKS,
  Protocols.HYSTERIA,
]);

const DEFAULT_PROBE_URL = 'https://www.google.com/generate_204';
const DEFAULT_PROBE_INTERVAL = '10s';

interface BalancerSettings {
  members?: number[];
  probeUrl?: string;
  probeInterval?: string;
  visibility?: Visibility;
  subIds?: string[];
}

export default function BalancerFormModal({
  open,
  mode,
  dbInbound,
  dbInbounds,
  onClose,
  onSaved,
}: BalancerFormModalProps) {
  const { t } = useTranslation();
  const [messageApi, messageContextHolder] = message.useMessage();
  const [remark, setRemark] = useState('');
  const [memberIds, setMemberIds] = useState<number[]>([]);
  const [probeUrl, setProbeUrl] = useState(DEFAULT_PROBE_URL);
  const [probeInterval, setProbeInterval] = useState(DEFAULT_PROBE_INTERVAL);
  const [visibility, setVisibility] = useState<Visibility>('all');
  const [selectedSubIds, setSelectedSubIds] = useState<string[]>([]);
  const [memberSubs, setMemberSubs] = useState<MemberSub[]>([]);
  const [loadingSubs, setLoadingSubs] = useState(false);
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    if (!open) return;
    if (mode === 'edit' && dbInbound) {
      const s = coerceInboundJsonField(dbInbound.settings) as { balancer?: BalancerSettings };
      const balancer = s.balancer || {};
      setRemark(dbInbound.remark || '');
      setMemberIds(Array.isArray(balancer.members) ? balancer.members : []);
      setProbeUrl(balancer.probeUrl || DEFAULT_PROBE_URL);
      setProbeInterval(balancer.probeInterval || DEFAULT_PROBE_INTERVAL);
      setVisibility(balancer.visibility === 'selected' ? 'selected' : 'all');
      setSelectedSubIds(Array.isArray(balancer.subIds) ? balancer.subIds : []);
    } else {
      setRemark('');
      setMemberIds([]);
      setProbeUrl(DEFAULT_PROBE_URL);
      setProbeInterval(DEFAULT_PROBE_INTERVAL);
      setVisibility('all');
      setSelectedSubIds([]);
    }
    setMemberSubs([]);
  }, [open, mode, dbInbound]);

  // Load candidate subscription IDs from the selected members whenever the
  // visibility picker needs them. Members drive the list, so refetch on change.
  useEffect(() => {
    if (!open || visibility !== 'selected' || memberIds.length === 0) {
      if (visibility !== 'selected') setMemberSubs([]);
      return;
    }
    let cancelled = false;
    setLoadingSubs(true);
    HttpUtil.post('/panel/api/inbounds/balancer/memberSubs', { memberIds }, {
      headers: { 'Content-Type': 'application/json' },
      silent: true,
    })
      .then((msg) => {
        if (cancelled) return;
        const rows = Array.isArray(msg?.obj) ? (msg.obj as MemberSub[]) : [];
        setMemberSubs(rows);
      })
      .finally(() => {
        if (!cancelled) setLoadingSubs(false);
      });
    return () => {
      cancelled = true;
    };
  }, [open, visibility, memberIds]);

  const memberOptions = useMemo(
    () =>
      dbInbounds
        .filter((ib) => ELIGIBLE.has(ib.protocol) && ib.nodeId == null)
        .map((ib) => ({ value: ib.id, label: formatInboundLabel(ib.tag, ib.remark) })),
    [dbInbounds],
  );

  // SubId options: candidates loaded from members, plus any already-selected
  // subIds not in that list (e.g. a member was removed after selection) so the
  // admin can still see and clear them. The label shows the client emails that
  // share the subId for readability.
  const subOptions = useMemo(() => {
    const ids = new Set(memberSubs.map((s) => s.subId));
    for (const id of selectedSubIds) ids.add(id);
    return Array.from(ids).map((subId) => {
      const emails = memberSubs.find((s) => s.subId === subId)?.emails ?? [];
      return { value: subId, label: emails.length > 0 ? `${subId} — ${emails.join(', ')}` : subId };
    });
  }, [memberSubs, selectedSubIds]);

  const submit = async () => {
    if (memberIds.length < 2) {
      messageApi.warning(t('pages.inbounds.balancer.needTwoMembers'));
      return;
    }
    setSaving(true);
    try {
      const payload = {
        remark,
        enable: dbInbound?.enable ?? true,
        protocol: Protocols.BALANCER,
        port: 0,
        settings: JSON.stringify({
          balancer: {
            members: memberIds,
            probeUrl: probeUrl || DEFAULT_PROBE_URL,
            probeInterval: probeInterval || DEFAULT_PROBE_INTERVAL,
            visibility,
            subIds: visibility === 'selected' ? selectedSubIds : [],
          },
        }),
        streamSettings: '',
        sniffing: '',
      };
      const url =
        mode === 'edit' && dbInbound
          ? `/panel/api/inbounds/balancer/update/${dbInbound.id}`
          : '/panel/api/inbounds/balancer/add';
      const msg = await HttpUtil.post(url, payload);
      if (msg?.success) {
        onSaved?.();
        onClose();
      }
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal
      open={open}
      title={mode === 'edit' ? t('pages.inbounds.balancer.editTitle') : t('pages.inbounds.balancer.createTitle')}
      okText={mode === 'edit' ? t('save') : t('pages.inbounds.balancer.create')}
      cancelText={t('cancel')}
      okButtonProps={{ disabled: memberIds.length < 2, loading: saving }}
      onCancel={onClose}
      onOk={submit}
      destroyOnHidden
    >
      {messageContextHolder}
      <Alert
        type="info"
        showIcon
        style={{ marginBottom: 16 }}
        message={t('pages.inbounds.balancer.hint')}
      />
      <Form layout="vertical">
        <Form.Item label={t('pages.inbounds.remark')}>
          <Input value={remark} onChange={(e) => setRemark(e.target.value)} placeholder="EU-Balancer" />
        </Form.Item>
        <Form.Item
          label={t('pages.inbounds.balancer.members')}
          required
          help={memberOptions.length === 0 ? t('pages.inbounds.balancer.noEligible') : undefined}
          validateStatus={memberIds.length > 0 && memberIds.length < 2 ? 'warning' : undefined}
        >
          <Select
            mode="multiple"
            style={{ width: '100%' }}
            value={memberIds}
            onChange={setMemberIds}
            options={memberOptions}
            placeholder={t('pages.inbounds.balancer.selectMembers')}
            optionFilterProp="label"
          />
        </Form.Item>
        <Form.Item label={t('pages.inbounds.balancer.visibility')}>
          <Radio.Group
            value={visibility}
            onChange={(e) => setVisibility(e.target.value as Visibility)}
            optionType="button"
            buttonStyle="solid"
            options={[
              { value: 'all', label: t('pages.inbounds.balancer.visibilityAll') },
              { value: 'selected', label: t('pages.inbounds.balancer.visibilitySelected') },
            ]}
          />
        </Form.Item>
        {visibility === 'selected' && (
          <Form.Item
            label={t('pages.inbounds.balancer.shownTo')}
            help={memberIds.length === 0 ? t('pages.inbounds.balancer.selectMembersFirst') : undefined}
          >
            <Select
              mode="multiple"
              style={{ width: '100%' }}
              value={selectedSubIds}
              onChange={setSelectedSubIds}
              options={subOptions}
              loading={loadingSubs}
              placeholder={t('pages.inbounds.balancer.selectSubs')}
              optionFilterProp="label"
              disabled={memberIds.length === 0}
            />
          </Form.Item>
        )}
        <Collapse
          ghost
          items={[
            {
              key: 'advanced',
              label: t('pages.inbounds.balancer.advanced'),
              children: (
                <>
                  <Form.Item label={t('pages.inbounds.balancer.probeUrl')}>
                    <Input value={probeUrl} onChange={(e) => setProbeUrl(e.target.value)} placeholder={DEFAULT_PROBE_URL} />
                  </Form.Item>
                  <Form.Item label={t('pages.inbounds.balancer.probeInterval')} style={{ marginBottom: 0 }}>
                    <Input value={probeInterval} onChange={(e) => setProbeInterval(e.target.value)} placeholder={DEFAULT_PROBE_INTERVAL} />
                  </Form.Item>
                </>
              ),
            },
          ]}
        />
      </Form>
    </Modal>
  );
}
