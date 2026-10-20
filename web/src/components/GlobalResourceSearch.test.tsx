import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'
import GlobalResourceSearch from './GlobalResourceSearch'
afterEach(cleanup)
it('keeps search open inside and dismisses outside or on Escape',async()=>{const user=userEvent.setup();const close=vi.fn();render(<GlobalResourceSearch open onClose={close} onOpenResult={vi.fn()}/>);await user.click(screen.getByLabelText('跨集群搜索'));expect(close).not.toHaveBeenCalled();await user.pointer({target:document.body,keys:'[MouseLeft]'});expect(close).toHaveBeenCalledTimes(1);close.mockClear();await user.keyboard('{Escape}');expect(close).toHaveBeenCalledTimes(1)})
