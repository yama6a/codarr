import { fireEvent, render, screen } from '@testing-library/react';
import { useState } from 'react';
import { SecretInput } from '../SecretInput';
import { SECRET_MASK } from '../../../api/types';

function Harness() {
  const [value, setValue] = useState(SECRET_MASK);
  return (
    <div>
      <SecretInput label="API key" value={value} onChange={setValue} />
      <output data-testid="sent">sent:{value}</output>
    </div>
  );
}

describe('SecretInput', () => {
  it('shows the mask and sends it back unchanged until the user types', () => {
    render(<Harness />);

    expect(screen.getByText(SECRET_MASK)).toBeInTheDocument();
    expect(screen.queryByLabelText('API key')).toBeNull();
    expect(screen.getByTestId('sent')).toHaveTextContent(`sent:${SECRET_MASK}`);
  });

  it('clears the field before the user types, so no stored secret is ever rendered', () => {
    render(<Harness />);

    fireEvent.click(screen.getByRole('button', { name: 'Change' }));
    const field = screen.getByLabelText('API key');
    expect(field).toHaveValue('');

    fireEvent.change(field, { target: { value: 'real-key' } });
    expect(screen.getByTestId('sent')).toHaveTextContent('sent:real-key');
  });

  it('restores the mask when the user backs out', () => {
    render(<Harness />);

    fireEvent.click(screen.getByRole('button', { name: 'Change' }));
    fireEvent.change(screen.getByLabelText('API key'), { target: { value: 'typo' } });
    fireEvent.click(screen.getByRole('button', { name: 'Keep the stored value' }));

    expect(screen.getByTestId('sent')).toHaveTextContent(`sent:${SECRET_MASK}`);
    expect(screen.queryByLabelText('API key')).toBeNull();
  });
});
