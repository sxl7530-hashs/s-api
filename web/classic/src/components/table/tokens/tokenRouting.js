/*
Copyright (C) 2025 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/

export const CUSTOM_PROFILE_VALUE = 'custom';

export const profileSelectValue = (profileId) => {
  const id = Number(profileId) || 0;
  return id > 0 ? `profile:${id}` : CUSTOM_PROFILE_VALUE;
};

export const routingFormValues = (token, defaultUseAutoGroup = false) => {
  const autoGroups = Array.isArray(token?.auto_groups) ? token.auto_groups : [];
  const usesSystemOrder =
    token?.group === 'auto' &&
    token?.auto_groups_mode !== 'custom' &&
    autoGroups.length === 0;

  let selectedGroups = [];
  if (token?.group === 'auto') {
    selectedGroups = autoGroups;
  } else if (token?.group) {
    selectedGroups = [token.group];
  }

  return {
    token_group_profile_id: profileSelectValue(token?.token_group_profile_id),
    routing_mode:
      usesSystemOrder || (!token && defaultUseAutoGroup) ? 'system' : 'custom',
    selected_groups: selectedGroups,
  };
};

export const normalizeTokenRouting = (values) => {
  const normalized = { ...values };
  const profileValue = String(values.token_group_profile_id || '');
  const profileId = profileValue.startsWith('profile:')
    ? Number(profileValue.slice('profile:'.length)) || 0
    : Number(profileValue) || 0;
  const selectedGroups = Array.isArray(values.selected_groups)
    ? values.selected_groups.filter(Boolean)
    : [];

  delete normalized.routing_mode;
  delete normalized.selected_groups;
  delete normalized.auto_groups_mode;
  normalized.token_group_profile_id = profileId;

  if (profileId > 0 || values.routing_mode === 'system') {
    normalized.group = 'auto';
    normalized.auto_groups = [];
    normalized.cross_group_retry = true;
    return normalized;
  }

  if (selectedGroups.length <= 1) {
    normalized.group = selectedGroups[0] || '';
    normalized.auto_groups = [];
    normalized.cross_group_retry = false;
    return normalized;
  }

  normalized.group = 'auto';
  normalized.auto_groups = selectedGroups;
  normalized.cross_group_retry = true;
  return normalized;
};
