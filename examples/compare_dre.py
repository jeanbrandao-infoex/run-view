"""Input /input/june.csv and /input/july.csv: account,description,value.
Example for already normalized detail accounts, not arbitrary PDF extraction.
Amounts use Decimal; never sum subtotals together with their detail rows.
"""
from decimal import Decimal
import csv
from pathlib import Path
from openpyxl import Workbook

def read_period(name):
    with open('/input/' + name, newline='', encoding='utf-8-sig') as file:
        rows = list(csv.DictReader(file))
    result = {}
    for row in rows:
        key = row['account']
        if key in result:
            raise ValueError('Duplicate account: ' + key)
        result[key] = (row['description'], Decimal(row['value']))
    return result

june = read_period('june.csv')
july = read_period('july.csv')
accounts = list(june) + [key for key in july if key not in june]
book = Workbook()
sheet = book.active
sheet.title = 'Comparativo'
sheet.append(['Conta', 'Descrição', 'Junho', 'Julho', 'Variação R$', 'Variação %'])
def text(value):
    # Neutralize spreadsheet formula injection from untrusted text.
    return "'" + value if value.lstrip().startswith(('=', '+', '-', '@')) else value
for key in accounts:
    description = july.get(key, june.get(key))[0]
    previous = june.get(key, ('', Decimal(0)))[1]
    current = july.get(key, ('', Decimal(0)))[1]
    delta = current - previous
    percent = None if previous == 0 else delta / abs(previous)
    sheet.append([text(key), text(description), float(previous), float(current), float(delta),
                  None if percent is None else float(percent)])
for row in sheet.iter_rows(min_row=2):
    for cell in row[2:5]:
        cell.number_format = '#,##0.00'
    row[5].number_format = '0.00%'
sheet.freeze_panes = 'A2'
sheet.auto_filter.ref = sheet.dimensions
book.save('/workspace/comparativo.xlsx')
print(f'{len(accounts)} contas comparadas. Base zero: percentual vazio. Ordem original preservada.')
