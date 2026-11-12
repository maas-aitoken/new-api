/*
Copyright (C) 2023-2026 QuantumNous

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
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import { PricingToolbar } from '../pricing-toolbar'

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}))

vi.mock('../pricing-sidebar', () => ({
  PricingSidebar: () => null,
}))

const defaultProps = {
  searchInput: '',
  onSearchChange: vi.fn(),
  onClearSearch: vi.fn(),
  filteredCount: 12,
  totalCount: 20,
  sortBy: 'name',
  onSortChange: vi.fn(),
  tokenUnit: 'M' as const,
  onTokenUnitChange: vi.fn(),
  showRechargePrice: false,
  onRechargePriceChange: vi.fn(),
  viewMode: 'card' as const,
  onViewModeChange: vi.fn(),
  quotaTypeFilter: 'all',
  endpointTypeFilter: 'all',
  vendorFilter: 'all',
  groupFilter: 'all',
  tagFilter: 'all',
  onQuotaTypeChange: vi.fn(),
  onEndpointTypeChange: vi.fn(),
  onVendorChange: vi.fn(),
  onGroupChange: vi.fn(),
  onTagChange: vi.fn(),
  vendors: [],
  groups: [],
  tags: [],
  models: [],
  hasActiveFilters: false,
  activeFilterCount: 0,
  onClearFilters: vi.fn(),
  filtersOpen: false,
  onFiltersOpenChange: vi.fn(),
  advancedFilters: {
    contextLength: 'all',
    parameterCount: 'all',
    releaseDate: 'all',
  },
  advancedOptions: {
    hasContextLength: false,
    hasParameterCount: false,
    hasReleaseDate: false,
  },
  onAdvancedFilterChange: vi.fn(),
}

describe('PricingToolbar', () => {
  it('reports the filter panel state and toggles it from the desktop control', async () => {
    const user = userEvent.setup()
    const onFiltersOpenChange = vi.fn()

    render(
      <PricingToolbar
        {...defaultProps}
        onFiltersOpenChange={onFiltersOpenChange}
      />
    )

    const toggle = screen.getByRole('button', {
      name: 'Filter',
      expanded: false,
    })
    expect(toggle).toHaveAttribute('aria-expanded', 'false')

    await user.click(toggle)

    expect(onFiltersOpenChange).toHaveBeenCalledWith(true)
  })
})
