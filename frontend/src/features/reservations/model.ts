export function reservationStatusLabel(status: string): string {
  return ({
    booked: '예약 완료',
    cancelled: '취소 완료',
    cancellation_pending: '취소 처리 중',
    prepared: '결제 준비',
    abandoned: '다시 찾는 중',
    expired: '결제 대기 종료',
    unknown: '결제 결과 확인 필요',
  } as Record<string, string>)[status] ?? status;
}

export function reservationReference(status: string, bookingNumber: string): string {
  if (bookingNumber) return bookingNumber;
  if (status === 'prepared') return '결제를 기다리는 중';
  if (status === 'abandoned') return '새 좌석을 찾기 위해 종료됨';
  if (status === 'expired') return '결제 대기 시간이 지나 종료됨';
  if (status === 'unknown') return 'CGV 예매 내역 확인 필요';
  return '예매번호 없음';
}
