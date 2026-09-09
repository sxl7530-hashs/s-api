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

import { describe, expect, test } from 'bun:test';
import { normalizeTokenRouting, routingFormValues } from '../tokenRouting';

describe('classic token group routing', () => {
  test.each([
    [[], '', [], false],
    [['default'], 'default', [], false],
    [['default', 'vip'], 'auto', ['default', 'vip'], true],
  ])(
    'maps selected groups %j to the backend routing contract',
    (selectedGroups, group, autoGroups, crossGroupRetry) => {
      const result = normalizeTokenRouting({
        token_group_profile_id: 'custom',
        routing_mode: 'custom',
        selected_groups: selectedGroups,
      });

      expect(result).toEqual({
        token_group_profile_id: 0,
        group,
        auto_groups: autoGroups,
        cross_group_retry: crossGroupRetry,
      });
    },
  );

  test('keeps system Auto separate from an empty custom selection', () => {
    const result = normalizeTokenRouting({
      token_group_profile_id: 'custom',
      routing_mode: 'system',
      selected_groups: [],
    });

    expect(result).toEqual({
      token_group_profile_id: 0,
      group: 'auto',
      auto_groups: [],
      cross_group_retry: true,
    });
  });

  test('maps a shortcut profile to its numeric backend id', () => {
    const result = normalizeTokenRouting({
      token_group_profile_id: 'profile:12',
      routing_mode: 'custom',
      selected_groups: ['default'],
    });

    expect(result).toEqual({
      token_group_profile_id: 12,
      group: 'auto',
      auto_groups: [],
      cross_group_retry: true,
    });
  });

  test('restores custom and system selections when editing', () => {
    expect(
      routingFormValues({
        token_group_profile_id: 0,
        group: 'auto',
        auto_groups_mode: 'custom',
        auto_groups: ['vip', 'default'],
      }),
    ).toMatchObject({
      token_group_profile_id: 'custom',
      routing_mode: 'custom',
      selected_groups: ['vip', 'default'],
    });
    expect(
      routingFormValues({
        token_group_profile_id: 3,
        group: 'auto',
        auto_groups: [],
      }),
    ).toMatchObject({
      token_group_profile_id: 'profile:3',
      routing_mode: 'system',
      selected_groups: [],
    });
  });
});
