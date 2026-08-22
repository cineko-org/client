export function reservationStatusLabel(status: string): string {
  return ({
    booked: '예약 완료',
    cancelled: '취소 완료',
    cancellationCommitting: '취소 처리 중',
    cancellationUnknown: '취소 결과 확인 필요',
    prepared: '결제 준비',
  } as Record<string, string>)[status] ?? status;
}

export function reservationReference(status: string, bookingNumber: string): string {
  if (bookingNumber) return bookingNumber;
  if (status === 'prepared') return '결제를 기다리는 중';
  if (status === 'cancellationCommitting') return '취소 처리를 확인하는 중';
  if (status === 'cancellationUnknown') return 'CGV 취소 내역 확인 필요';
  return '예매번호 없음';
}
