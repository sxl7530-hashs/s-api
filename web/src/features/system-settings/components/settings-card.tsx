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
import { memo } from 'react'

import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
  CardDescription,
} from '@/components/ui/card'
import { cn } from '@/lib/utils'

type SettingsCardProps = {
  title: string
  description?: string
  children: React.ReactNode
  className?: string
}

export const SettingsCard = memo(function SettingsCard(
  props: SettingsCardProps
) {
  return (
    <Card
      data-card-hover='false'
      className={cn(
        'gap-0 overflow-hidden rounded-2xl border-border/70 bg-card py-0 shadow-[0_14px_32px_-28px_rgba(6,78,59,0.6)]',
        props.className
      )}
    >
      <CardHeader className='border-b border-border/70 bg-[linear-gradient(90deg,color-mix(in_oklch,var(--primary)_8%,var(--card)),var(--card)_72%)] px-4 py-4 sm:px-5'>
        <CardTitle className='text-[15px] font-semibold tracking-tight text-foreground'>
          {props.title}
        </CardTitle>
        {props.description && (
          <CardDescription className='max-w-3xl text-[13px] leading-relaxed text-muted-foreground'>
            {props.description}
          </CardDescription>
        )}
      </CardHeader>
      <CardContent className='bg-background/35 p-4 sm:p-5'>{props.children}</CardContent>
    </Card>
  )
})
